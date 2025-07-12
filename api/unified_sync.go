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

	// 立即返回响应，避免HTTP请求context影响后续处理
	utils.SendResponse(c, nil, responseMsg)

	// 创建完全独立的context，不受HTTP请求影响
	independentCtx := context.Background()

	// 获取超时时间
	timeoutMinutes := viper.GetInt("sync.timeout_minutes")
	if timeoutMinutes <= 0 {
		timeoutMinutes = 25 // 默认25分钟
	}

	// 启动完全独立的后台处理协程，使用SafeGoroutineManager
	// 使用更长的超时时间确保不会过早取消
	parentTimeout := time.Duration(timeoutMinutes+15) * time.Minute // 增加15分钟缓冲
	log.Printf("[统一同步] 启动主协程，超时时间: %v", parentTimeout)
	err = utils.GlobalSafeGoroutineManager.StartSafeGoroutineWithTimeout(
		independentCtx,
		fmt.Sprintf("unified-sync-node-%d", req.Node),
		parentTimeout,
		func(ctx context.Context) {
			log.Printf("[统一同步] 启动独立的后台处理协程，节点: %d", req.Node)

			// 创建带缓冲的结果通道，防止阻塞
			results := make(chan UnifiedSyncResult, accountCount+10) // 增加缓冲

			// 创建安全的WaitGroup
			type SafeWaitGroup struct {
				wg sync.WaitGroup
				mu sync.Mutex
			}

			swg := &SafeWaitGroup{}

			// 为每个账号启动一个协程
			for i, account := range filteredAccounts {
				accountIndex := i + 1

				// 使用SafeGoroutineManager启动协程
				childTimeout := time.Duration(timeoutMinutes) * time.Minute
				log.Printf("[统一同步] 准备启动账号 %d 协程，超时时间: %v", account.ID, childTimeout)
				err := utils.GlobalSafeGoroutineManager.StartSafeGoroutineWithTimeout(
					independentCtx, // 使用独立的context，避免父context取消影响子协程
					fmt.Sprintf("account-sync-%d", account.ID),
					childTimeout,
					func(accCtx context.Context) {
						swg.mu.Lock()
						swg.wg.Add(1)
						swg.mu.Unlock()

						defer func() {
							// 完成时减少全局计数
							atomic.AddInt32(&currentUnifiedSyncs, -1)
							log.Printf("[统一同步] 账号 %d 协程完成，剩余全局协程数: %d",
								account.ID, atomic.LoadInt32(&currentUnifiedSyncs))

							// 安全地调用Done
							swg.mu.Lock()
							swg.wg.Done()
							swg.mu.Unlock()
						}()

						log.Printf("[统一同步] 账号 %d (%s) 协程开始处理 [%d/%d]", account.ID, account.Account, accountIndex, accountCount)

						// 检查context状态
						select {
						case <-accCtx.Done():
							log.Printf("[统一同步] 账号 %d 协程启动时context已取消: %v", account.ID, accCtx.Err())
							// 即使context被取消也要更新状态
							if updateErr := model.ResetSyncTimeOnFailure(account.ID); updateErr != nil {
								log.Printf("[统一同步] 重置账号 %d 状态失败: %v", account.ID, updateErr)
							}
							return
						default:
							// context正常，继续处理
							log.Printf("[统一同步] 账号 %d 协程context状态正常，开始处理", account.ID)
						}

						// 执行统一同步（先列表，后详情）
						result := syncSingleAccountSequential(account, req, accCtx)

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

						// 安全发送结果，防止阻塞和向已关闭通道发送
						func() {
							defer func() {
								if r := recover(); r != nil {
									log.Printf("[统一同步] 账号 %d 发送结果时发生panic: %v", account.ID, r)
									// 即使发送失败也不影响程序运行
								}
							}()

							// 尝试发送结果，如果失败也不要阻塞
							select {
							case results <- result:
								// 成功发送
								log.Printf("[统一同步] 账号 %d 结果发送成功", account.ID)
							case <-accCtx.Done():
								// 超时或取消
								log.Printf("[统一同步] 账号 %d 结果发送被取消: %v", account.ID, accCtx.Err())
							case <-time.After(5 * time.Second):
								// 发送超时，不等太久
								log.Printf("[统一同步] 账号 %d 结果发送超时，跳过发送", account.ID)
							}
						}()

						log.Printf("[统一同步] 账号 %d (%s) 协程处理完成", account.ID, account.Account)
					},
				)

				if err != nil {
					log.Printf("[统一同步] 启动账号 %d 协程失败: %v", account.ID, err)
					// 启动失败时减少计数
					atomic.AddInt32(&currentUnifiedSyncs, -1)
				}
			}

			// 启动结果收集协程
			collectorTimeout := time.Duration(timeoutMinutes+20) * time.Minute // 给结果收集更多时间
			log.Printf("[统一同步] 启动结果收集协程，超时时间: %v", collectorTimeout)
			err := utils.GlobalSafeGoroutineManager.StartSafeGoroutineWithTimeout(
				independentCtx, // 使用独立的context
				fmt.Sprintf("result-collector-node-%d", req.Node),
				collectorTimeout,
				func(collectorCtx context.Context) {
					// 收集结果的计数器
					var successCount, failureCount int
					totalListCount, totalContentCount := 0, 0
					processedCount := 0

					// 等待所有协程完成或超时
					waitDone := make(chan struct{})
					go func() {
						swg.wg.Wait()
						close(waitDone)
					}()

					// 在单独的协程中收集结果
					go func() {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("[统一同步] 结果收集协程panic: %v", r)
							}
						}()

						for {
							select {
							case result, ok := <-results:
								if !ok {
									// 通道已关闭
									return
								}
								processedCount++
								if result.Error != nil {
									failureCount++
									log.Printf("[统一同步] 账号 %d 处理失败: %v", result.AccountID, result.Error)
								} else {
									successCount++
									totalListCount += result.ListCount
									totalContentCount += result.ContentCount
								}
							case <-collectorCtx.Done():
								log.Printf("[统一同步] 结果收集被取消: %v", collectorCtx.Err())
								return
							}
						}
					}()

					// 等待所有协程完成，给足够的时间
					maxWaitTime := time.Duration(timeoutMinutes+5) * time.Minute
					log.Printf("[统一同步] 等待所有账号协程完成，最大等待时间: %v", maxWaitTime)

					select {
					case <-waitDone:
						log.Printf("[统一同步] 所有账号协程正常完成")
					case <-collectorCtx.Done():
						log.Printf("[统一同步] 结果收集被取消: %v", collectorCtx.Err())
					case <-time.After(maxWaitTime):
						log.Printf("[统一同步] 等待协程完成超时，强制结束")
					}

					log.Printf("[统一同步] 准备关闭results通道")
					// 关闭通道（在所有协程完成后）
					close(results)
					log.Printf("[统一同步] results通道已关闭")

					// 给结果收集一点时间
					time.Sleep(time.Second * 2)

					log.Printf("[统一同步] 节点 %d 处理完成 - 成功: %d, 失败: %d, 总邮件列表: %d, 总邮件内容: %d, 收集到结果: %d",
						req.Node, successCount, failureCount, totalListCount, totalContentCount, processedCount)
				},
			)

			if err != nil {
				log.Printf("[统一同步] 启动结果收集协程失败: %v", err)
			}
		},
	)

	if err != nil {
		log.Printf("[统一同步] 启动后台处理协程失败: %v", err)
		// 启动失败时重置所有计数
		atomic.AddInt32(&currentUnifiedSyncs, -int32(accountCount))

		// 重置账号状态
		for _, account := range filteredAccounts {
			if resetErr := model.ResetSyncTimeOnFailure(account.ID); resetErr != nil {
				log.Printf("[统一同步] 重置账号 %d 状态失败: %v", account.ID, resetErr)
			}
		}
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
