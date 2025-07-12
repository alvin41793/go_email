package api

import (
	"context"
	"fmt"
	"go_email/model"
	"go_email/pkg/utils"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// UnifiedSyncRequest 统一同步请求
type UnifiedSyncRequest struct {
	SyncLimit int `json:"sync_limit"`              // 每个账号同步的邮件数量（列表和详情统一）
	Node      int `json:"node" binding:"required"` // 节点编号，用于筛选特定节点的账号（必填）
}

// 统一同步相关的全局变量
var (
	unifiedSyncMutex    sync.Mutex
	currentUnifiedSyncs int32      // 当前统一同步的协程数
	maxUnifiedSyncs     int32 = 20 // 最大统一同步协程数（调整为20以支持更多账号）
)

// UnifiedEmailSync 统一邮件同步接口
// 每个账号开启一个协程，先同步邮件列表，再同步邮件详情
func UnifiedEmailSync(c *gin.Context) {
	var req UnifiedSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
		return
	}

	// 检查节点参数是否有效
	if req.Node <= 0 {
		utils.SendResponse(c, fmt.Errorf("节点编号必须大于0，当前值: %d", req.Node), "节点编号无效")
		return
	}

	// 设置默认值
	if req.SyncLimit <= 0 {
		req.SyncLimit = 30 // 默认每个账号同步30封邮件
	}

	// 使用互斥锁保护并发访问
	unifiedSyncMutex.Lock()

	// 检查是否已达到最大协程数
	if atomic.LoadInt32(&currentUnifiedSyncs) >= maxUnifiedSyncs {
		unifiedSyncMutex.Unlock()
		utils.SendResponse(c, nil, "已达到最大统一同步协程数量，请等待当前任务完成")
		return
	}

	// 原子性地获取账号并立即更新状态，防止并发竞争
	// 计算可用的协程槽位
	remainingSlots := maxUnifiedSyncs - atomic.LoadInt32(&currentUnifiedSyncs)
	maxAccounts := int(remainingSlots) // 每个账号一个协程

	filteredAccounts, err := model.GetAndUpdateAccountsForUnifiedSync(req.Node, maxAccounts)
	if err != nil {
		unifiedSyncMutex.Unlock()
		utils.SendResponse(c, err, "获取邮箱配置失败")
		return
	}

	if len(filteredAccounts) == 0 {
		unifiedSyncMutex.Unlock()
		utils.SendResponse(c, nil, fmt.Sprintf("没有找到节点 %d 的可用邮箱账号（可能都在处理中）", req.Node))
		return
	}

	// 每个账号创建一个协程
	accountCount := len(filteredAccounts)

	// 更新全局协程计数
	atomic.AddInt32(&currentUnifiedSyncs, int32(accountCount))

	log.Printf("[统一同步] 节点 %d - 获取了 %d 个账号，将为每个账号创建一个协程", req.Node, accountCount)
	fmt.Printf("[统一同步] 节点 %d - 获取了 %d 个账号，将为每个账号创建一个协程\n", req.Node, accountCount)

	unifiedSyncMutex.Unlock()

	// 构造返回消息
	responseMsg := fmt.Sprintf("正在统一同步节点 %d 的 %d 个邮箱账号，每个账号创建一个协程，同步 %d 封邮件，当前全局协程数: %d",
		req.Node, accountCount, req.SyncLimit, atomic.LoadInt32(&currentUnifiedSyncs))

	// 返回正在处理的信息
	utils.SendResponse(c, nil, responseMsg)

	// 创建结果通道
	results := make(chan UnifiedSyncResult, accountCount)

	// 启动工作池
	var wg sync.WaitGroup

	// 使用安全协程管理器启动后台处理
	syncCtx := context.Background()

	syncErr := utils.GlobalSafeGoroutineManager.StartSafeGoroutine(syncCtx, "unified-sync-batch", func(ctx context.Context) {
		// 为每个账号启动一个协程
		for i, account := range filteredAccounts {
			wg.Add(1)
			accountIndex := i + 1

			accountErr := utils.GlobalSafeGoroutineManager.StartSafeGoroutine(ctx, fmt.Sprintf("unified-sync-account-%d", account.ID), func(ctx context.Context) {
				defer wg.Done()
				defer func() {
					// 完成时减少全局计数
					atomic.AddInt32(&currentUnifiedSyncs, -1)
					log.Printf("[统一同步] 账号 %d 协程完成，剩余全局协程数: %d",
						account.ID, atomic.LoadInt32(&currentUnifiedSyncs))

					// 捕获panic
					if r := recover(); r != nil {
						log.Printf("[统一同步] 账号 %d 协程发生panic: %v", account.ID, r)
					}
				}()

				log.Printf("[统一同步] 账号 %d (%s) 协程开始处理 [%d/%d]", account.ID, account.Account, accountIndex, accountCount)

				// 创建超时context，从配置文件读取超时时间
				timeoutMinutes := viper.GetInt("sync.timeout_minutes")
				if timeoutMinutes <= 0 {
					timeoutMinutes = 30 // 默认30分钟
				}
				timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Duration(timeoutMinutes)*time.Minute)
				log.Printf("[统一同步] 账号 %d 设置超时时间: %d 分钟", account.ID, timeoutMinutes)

				// 执行统一同步（先列表，后详情）
				result := syncSingleAccountSequential(account, req, timeoutCtx)

				// 清理超时context
				timeoutCancel()

				// 根据处理结果更新账号状态
				if result.Error != nil {
					// 处理失败，重置同步时间
					if updateErr := model.ResetSyncTimeOnFailure(account.ID); updateErr != nil {
						log.Printf("[统一同步] 重置账号 %d 状态失败: %v", account.ID, updateErr)
					} else {
						log.Printf("[统一同步] 账号 %d 处理失败，已重置状态", account.ID)
					}
				} else {
					// 处理成功，更新为完成时间
					if updateErr := model.UpdateLastSyncTimeOnComplete(account.ID); updateErr != nil {
						log.Printf("[统一同步] 更新账号 %d 完成状态失败: %v", account.ID, updateErr)
					} else {
						log.Printf("[统一同步] 账号 %d 处理完成，已更新状态", account.ID)
					}
				}

				results <- result
				log.Printf("[统一同步] 账号 %d (%s) 协程处理完成", account.ID, account.Account)
			})

			if accountErr != nil {
				log.Printf("[统一同步] 创建账号 %d 协程失败: %v", account.ID, accountErr)
				wg.Done()
				atomic.AddInt32(&currentUnifiedSyncs, -1)
				continue
			}
		}

		// 收集结果
		resultErr := utils.GlobalSafeGoroutineManager.StartSafeGoroutine(ctx, "unified-sync-results", func(ctx context.Context) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[统一同步] 结果收集发生panic: %v", r)
				}
			}()

			// 等待所有协程完成
			wg.Wait()
			close(results)

			// 统计结果
			var successCount, failureCount int
			totalListCount, totalContentCount := 0, 0

			for result := range results {
				if result.Error != nil {
					failureCount++
					log.Printf("[统一同步] 账号 %d 处理失败: %v", result.AccountID, result.Error)
				} else {
					successCount++
					totalListCount += result.ListCount
					totalContentCount += result.ContentCount
				}
			}

			log.Printf("[统一同步] 节点 %d 处理完成 - 成功: %d, 失败: %d, 总邮件列表: %d, 总邮件内容: %d",
				req.Node, successCount, failureCount, totalListCount, totalContentCount)
		})

		if resultErr != nil {
			log.Printf("[统一同步] 创建结果收集协程失败: %v", resultErr)
		}
	})

	if syncErr != nil {
		log.Printf("[统一同步] 创建批处理协程失败: %v", syncErr)
		// 重置协程计数
		atomic.AddInt32(&currentUnifiedSyncs, -int32(accountCount))
	}
}

// UnifiedSyncResult 统一同步结果
type UnifiedSyncResult struct {
	AccountID    int
	Error        error
	ListCount    int // 同步的邮件列表数量
	ContentCount int // 同步的邮件内容数量
}

// syncSingleAccountSequential 顺序同步单个账号（先列表，后详情）
func syncSingleAccountSequential(account model.PrimeEmailAccount, req UnifiedSyncRequest, ctx context.Context) UnifiedSyncResult {
	result := UnifiedSyncResult{
		AccountID: account.ID,
	}

	log.Printf("[账号同步] 开始处理账号 %d (%s)", account.ID, account.Account)

	// 创建邮件客户端
	mailClient, err := newMailClient(account)
	if err != nil {
		result.Error = fmt.Errorf("创建邮件客户端失败: %v", err)
		return result
	}

	// 第一步：同步邮件列表
	log.Printf("[账号同步] 账号 %d - 开始同步邮件列表，数量限制: %d", account.ID, req.SyncLimit)
	listCount, err := syncAccountEmailList(mailClient, account, req.SyncLimit, ctx)
	if err != nil {
		result.Error = fmt.Errorf("同步邮件列表失败: %v", err)
		return result
	}
	result.ListCount = listCount
	log.Printf("[账号同步] 账号 %d - 邮件列表同步完成，数量: %d", account.ID, listCount)

	// 第二步：同步邮件详情
	log.Printf("[账号同步] 账号 %d - 开始同步邮件详情，数量限制: %d", account.ID, req.SyncLimit)
	contentCount, err := syncAccountEmailContent(mailClient, account, req.SyncLimit, ctx)
	if err != nil {
		result.Error = fmt.Errorf("同步邮件详情失败: %v", err)
		return result
	}
	result.ContentCount = contentCount
	log.Printf("[账号同步] 账号 %d - 邮件详情同步完成，数量: %d", account.ID, contentCount)

	log.Printf("[账号同步] 账号 %d (%s) 处理完成 - 列表: %d, 详情: %d",
		account.ID, account.Account, result.ListCount, result.ContentCount)

	return result
}
