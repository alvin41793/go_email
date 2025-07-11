package api

import (
	"errors"
	"fmt"
	"go_email/db"
	"go_email/model"
	"go_email/pkg/mailclient"
	"go_email/pkg/utils"
	"go_email/pkg/utils/oss"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 邮件服务器配置
var mailConfig struct {
	IMAPServer   string
	SMTPServer   string
	EmailAddress string
	Password     string
	IMAPPort     int
	SMTPPort     int
	UseSSL       bool
}

// 添加邮件列表操作的互斥锁
var (
	// 添加获取邮件列表处理相关的全局变量
	emailListProcessMutex          sync.Mutex
	currentEmailListGoroutines     int32     // 当前获取邮件列表运行的协程总数
	maxEmailListTotalGoroutines    int32 = 5 // 全局获取邮件列表最大协程数
	emailContentProcessMutex       sync.Mutex
	currentEmailContentGoroutines  int32      // 当前获取邮件内容运行的协程总数
	maxEmailContentTotalGoroutines int32 = 16 // 全局获取邮件内容最大协程数（支持16个账号）
	listEmailsByUidMutex           sync.Mutex
	goroutinesPerReq               int32 = 5 // 每次请求创建的协程数（已废弃，现在动态创建）
	sleepTime                      int   = 1 // 减少协程创建间隔时间
	processingAccounts             map[int]bool
)

// 初始化邮件配置
func InitMailClient(imapServer, smtpServer, emailAddress, password string, imapPort, smtpPort int, useSSL bool) {
	mailConfig.IMAPServer = imapServer
	mailConfig.SMTPServer = smtpServer
	mailConfig.EmailAddress = emailAddress
	mailConfig.password: REDACTED
	mailConfig.IMAPPort = imapPort
	mailConfig.SMTPPort = smtpPort
	mailConfig.UseSSL = useSSL
}

// 获取新的邮件客户端实例
func newMailClient(account model.PrimeEmailAccount) (*mailclient.MailClient, error) {
	// 每次都从数据库获取最新的邮箱配置
	emailConfig, err := mailclient.GetEmailConfig(account)
	if err != nil {
		log.Printf("获取邮箱配置失败: %v", err)
		return nil, err
	}

	// 使用从数据库获取的最新配置创建邮件客户端
	return mailclient.NewMailClient(emailConfig), nil
}

func GetEmailContentList(c *gin.Context) {
	var req GetEmailContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
		return
	}

	// 使用互斥锁保护并发访问
	emailContentProcessMutex.Lock()

	// 检查是否已达到最大协程数
	if atomic.LoadInt32(&currentEmailContentGoroutines) >= maxEmailContentTotalGoroutines {
		emailContentProcessMutex.Unlock()
		utils.SendResponse(c, nil, "已达到最大处理协程数量，请等待当前任务完成")
		return
	}

	// 计算本次请求可以创建的协程数量
	remainingSlots := maxEmailContentTotalGoroutines - atomic.LoadInt32(&currentEmailContentGoroutines)

	nodeInfo := ""
	if req.Node > 0 {
		nodeInfo = fmt.Sprintf("节点 %d ", req.Node)
	}

	log.Printf("[邮件处理] %s当前已有 %d 个协程，剩余协程槽位: %d",
		nodeInfo, atomic.LoadInt32(&currentEmailContentGoroutines), remainingSlots)

	// 释放互斥锁，允许其他请求继续
	emailContentProcessMutex.Unlock()

	// 【关键修改】获取所有可用账号，每个账号将创建一个协程
	allAccounts, err := model.GetAndUpdateAccountsForContent(req.Node, int(remainingSlots)) // 获取剩余槽位数量的账号
	if err != nil {
		log.Printf("[邮件处理] 获取账号失败: %v", err)
		utils.SendResponse(c, err, "获取账号失败")
		return
	}

	if len(allAccounts) == 0 {
		log.Printf("[邮件处理] 节点 %d - 没有找到活跃账号，可能都在处理中", req.Node)
		utils.SendResponse(c, nil, fmt.Sprintf("节点 %d 当前没有可用账号，请稍后再试（可能所有账号都在处理中）", req.Node))
		return
	}

	// 协程数量 = 获取到的账号数量（每个账号一个协程）
	createCount := int32(len(allAccounts))

	log.Printf("[邮件处理] 节点 %d - 获取了 %d 个账号，将创建 %d 个协程（每个账号一个协程）", req.Node, len(allAccounts), createCount)

	// 确认有账号可用后，返回准确的响应
	responseMsg := fmt.Sprintf("节点 %d 的邮件处理任务已启动，获取了 %d 个账号，正在创建 %d 个处理协程（每个账号一个协程）", req.Node, len(allAccounts), createCount)
	utils.SendResponse(c, nil, responseMsg)

	// 后台启动协程处理邮件
	go func() {
		// 使用WaitGroup来等待本次创建的协程完成
		var wg sync.WaitGroup

		// 创建结果通道
		results := make(chan error, createCount)

		// 启动协程以处理结果
		go func() {
			for err := range results {
				if err != nil {
					log.Printf("[邮件处理] 处理邮件时出错: %v", err)
				}
			}
		}()

		log.Printf("[邮件处理] 节点 %d - 开始为 %d 个账号分别创建协程", req.Node, len(allAccounts))

		// 【关键修改】为每个账号创建一个独立的协程
		for i, account := range allAccounts {
			wg.Add(1)

			// 增加全局协程计数
			currentCount := atomic.AddInt32(&currentEmailContentGoroutines, 1)

			log.Printf("[邮件处理] 创建协程 %d/%d (全局: %d/%d) 处理账号: %s (ID: %d)",
				i+1, len(allAccounts), currentCount, maxEmailContentTotalGoroutines, account.Account, account.ID)

			// 启动协程处理单个账号的邮件
			go func(goroutineNum int, account model.PrimeEmailAccount) {
				defer wg.Done()
				defer func() {
					// 完成时减少计数
					newCount := atomic.AddInt32(&currentEmailContentGoroutines, -1)
					log.Printf("[邮件处理] 协程 %d (账号: %s) 完成处理，剩余协程: %d",
						goroutineNum, account.Account, newCount)
				}()

				// 【关键】添加panic处理，确保账号状态能被重置
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[邮件处理] 协程 %d (账号: %s) 发生panic，正在重置账号状态: %v", goroutineNum, account.Account, r)
						// 发生panic时，重置该账号的状态
						if err := model.ResetSyncContentTimeOnFailure(account.ID); err != nil {
							log.Printf("[邮件处理] 重置账号 %d 状态失败: %v", account.ID, err)
						} else {
							log.Printf("[邮件处理] 已重置账号 %d 状态", account.ID)
						}
						// 将panic作为错误发送到results通道
						results <- fmt.Errorf("协程panic (账号: %s): %v", account.Account, r)
					}
				}()

				nodeInfo := ""
				if req.Node > 0 {
					nodeInfo = fmt.Sprintf("节点 %d ", req.Node)
				}
				log.Printf("[邮件处理] %s协程 %d 开始处理账号: %s (ID: %d)",
					nodeInfo, goroutineNum, account.Account, account.ID)

				// 调用处理函数，传入单个账号（包装成切片）
				singleAccountSlice := []model.PrimeEmailAccount{account}
				err := GetEmailContentWithAccounts(req.Limit, req.Node, singleAccountSlice)
				results <- err
			}(i+1, account)

			// 减少协程创建间隔时间（现在是1秒）
			if i < len(allAccounts)-1 { // 最后一个协程不需要等待
				time.Sleep(time.Duration(sleepTime) * time.Second)
			}
		}

		// 等待所有协程完成
		wg.Wait()
		close(results)
		nodeInfoForComplete := ""
		if req.Node > 0 {
			nodeInfoForComplete = fmt.Sprintf("节点 %d ", req.Node)
		}
		log.Printf("[邮件处理] %s本次请求创建的 %d 个协程已全部完成", nodeInfoForComplete, createCount)
	}()
}

// GetEmailContent 获取邮件内容
func GetEmailContent(limit int, node int) error {
	// 第一步：原子性地获取账号并立即更新同步时间，防止并发竞争
	accounts, err := model.GetAndUpdateAccountsForContent(node, 5)
	if err != nil {
		return err
	}

	if len(accounts) == 0 {
		log.Printf("[邮件处理] 节点 %d - 没有找到活跃账号", node)
		fmt.Println("没有找到活跃账号")
		return nil
	}

	log.Printf("[邮件处理] 节点 %d - 原子性获取并更新了 %d 个账号的同步时间", node, len(accounts))
	fmt.Printf("========== 节点 %d - 开始处理 %d 个账号的邮件 ==========\n", node, len(accounts))

	// 第二步：为每个账号获取邮件
	var allEmailIDs []model.PrimeEmail
	perAccountLimit := limit / len(accounts)
	remainder := limit % len(accounts)

	// 记录处理的账号信息
	processedAccounts := make(map[int]string)

	for i, account := range accounts {
		currentLimit := perAccountLimit
		// 将余数分配给前面的账号
		if i < remainder {
			currentLimit++
		}

		if currentLimit == 0 {
			continue
		}

		// 获取该账号的邮件
		accountEmails, err := model.GetEmailByStatusAndAccount(-1, account.ID, currentLimit)
		if err != nil {
			log.Printf("[邮件处理] 获取账号 %d 的邮件失败: %v", account.ID, err)
			continue
		}

		if len(accountEmails) > 0 {
			allEmailIDs = append(allEmailIDs, accountEmails...)
			processedAccounts[account.ID] = account.Account
			log.Printf("[邮件处理] 账号 %d (%s) - 获取到 %d 封待处理邮件", account.ID, account.Account, len(accountEmails))
			fmt.Printf("账号 %d (%s) - 获取到 %d 封待处理邮件\n", account.ID, account.Account, len(accountEmails))
		}
	}

	// 检查是否有邮件需要处理
	if len(allEmailIDs) == 0 {
		log.Printf("[邮件处理] 没有需要处理的新邮件")
		fmt.Println("没有需要处理的新邮件")
		return nil
	}

	emailIDs := allEmailIDs
	folder := "INBOX"

	log.Printf("[邮件处理] 开始处理 %d 封邮件, 文件夹: %s", len(emailIDs), folder)
	fmt.Printf("\n========== 开始处理 %d 封邮件，文件夹: %s ==========\n", len(emailIDs), folder)

	// 存储所有邮件内容和附件，以便后续批量存储
	type EmailData struct {
		EmailID      int
		AccountId    int
		EmailContent *model.PrimeEmailContent
		Attachments  []*model.PrimeEmailContentAttachment
	}

	allEmailData := make([]EmailData, 0, len(emailIDs))

	// 添加计数器
	var successCount, failureCount int

	// 第一步：获取所有邮件内容
	fmt.Printf("\n【第1阶段】获取所有邮件内容...\n")
	for i, emailOne := range emailIDs {
		log.Printf("[邮件处理] 正在获取邮件内容，ID: %d", emailOne.EmailID)
		fmt.Printf("  • 获取邮件 ID: %d 内容... ", emailOne.EmailID)

		// 在处理每个邮件之间添加延迟，避免连接过于频繁
		if i > 0 {
			time.Sleep(time.Millisecond * 500) // 500毫秒延迟
		}

		account, err := model.GetAccountByID(emailOne.AccountId)
		if err != nil && err != gorm.ErrRecordNotFound {
			log.Printf("[邮件处理] 获取邮件账号失败，ID: %d", emailOne.AccountId)
			fmt.Printf("  • 获取邮件账号失败，ID: %d", emailOne.AccountId)
			failureCount++
			continue
		}
		// 为每个请求创建独立的邮件客户端实例
		mailClient, err := newMailClient(account)
		if err != nil {
			log.Printf("[邮件处理] 获取邮箱配置失败: 账号ID=%d, 错误: %v", account.ID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			failureCount++
			// 设置邮件状态为失败
			resetErr := model.ResetEmailStatus(emailOne.EmailID, -2)
			if resetErr != nil {
				log.Printf("[邮件处理] 设置邮件状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
			}
			continue
		}
		email, err := mailClient.GetEmailContent(uint32(emailOne.EmailID), folder)
		if err != nil {
			log.Printf("[邮件处理] 获取邮件内容失败，邮件ID: %d, 错误: %v", emailOne.EmailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			failureCount++

			// 特殊处理：如果是UID不存在的错误，将邮件标记为已删除状态
			if strings.Contains(strings.ToLower(err.Error()), "邮件不存在") ||
				strings.Contains(strings.ToLower(err.Error()), "邮件uid无效") ||
				strings.Contains(strings.ToLower(err.Error()), "bad sequence") {
				log.Printf("[邮件处理] 检测到邮件已删除或UID无效，标记为已删除状态: 邮件ID=%d", emailOne.EmailID)
				resetErr := model.ResetEmailStatus(emailOne.EmailID, -3) // -3表示已删除
				if resetErr != nil {
					log.Printf("[邮件处理] 设置邮件已删除状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
				}
			} else if strings.Contains(strings.ToLower(err.Error()), "server error") ||
				strings.Contains(strings.ToLower(err.Error()), "please try again later") ||
				strings.Contains(strings.ToLower(err.Error()), "service unavailable") ||
				strings.Contains(strings.ToLower(err.Error()), "temporary failure") ||
				strings.Contains(strings.ToLower(err.Error()), "server busy") {
				// SELECT服务器临时错误，将状态回滚为-1以便重新处理
				log.Printf("[邮件处理] 检测到服务器临时错误，回滚状态为待处理: 邮件ID=%d, 错误=%v", emailOne.EmailID, err)
				resetErr := model.ResetEmailStatus(emailOne.EmailID, -1) // -1表示待处理，可以重新尝试
				if resetErr != nil {
					log.Printf("[邮件处理] 回滚邮件状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
				}
			} else {
				// 其他错误，设置为失败状态
				resetErr := model.ResetEmailStatus(emailOne.EmailID, -2)
				if resetErr != nil {
					log.Printf("[邮件处理] 设置邮件状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
				}
			}
			// 继续处理下一个邮件，而不是直接返回错误
			continue
		}

		log.Printf("[邮件处理] 成功获取邮件内容，邮件ID: %d, 主题: %s, 发件人: %s", emailOne.EmailID, email.Subject, email.From)
		fmt.Printf("✅ 成功，主题: %s\n", email.Subject)
		successCount++

		// 创建邮件内容记录
		emailContent := &model.PrimeEmailContent{
			EmailID:       emailOne.EmailID,
			AccountId:     emailOne.AccountId,
			Subject:       utils.SanitizeUTF8(email.Subject),
			FromEmail:     utils.SanitizeUTF8(email.From),
			ToEmail:       utils.SanitizeUTF8(email.To),
			Date:          utils.SanitizeUTF8(email.Date),
			Content:       utils.SanitizeUTF8(email.Body),
			HTMLContent:   utils.SanitizeUTF8(email.BodyHTML),
			Type:          0,
			HasAttachment: emailOne.HasAttachment,
			CreatedAt:     utils.JsonTime{Time: time.Now()},
			UpdatedAt:     utils.JsonTime{Time: time.Now()},
		}

		// 创建附件记录列表
		attachmentRecords := make([]*model.PrimeEmailContentAttachment, 0)
		if len(email.Attachments) > 0 {
			log.Printf("[邮件处理] 邮件含有 %d 个附件，邮件ID: %d", len(email.Attachments), emailOne.EmailID)
			fmt.Printf("    📎 发现 %d 个附件\n", len(email.Attachments))

			// 处理附件
			for i, attachment := range email.Attachments {
				log.Printf("[附件处理] 开始处理附件 %d/%d，邮件ID: %d, 文件名: %s",
					i+1, len(email.Attachments), emailOne.EmailID, attachment.Filename)
				fmt.Printf("      - 附件 %d/%d: %s (%.2f KB, %s)\n",
					i+1, len(email.Attachments), attachment.Filename, attachment.SizeKB, attachment.MimeType)

				// 上传到OSS
				ossURL := ""
				if attachment.Base64Data != "" {
					fileType := ""
					if attachment.MimeType != "" {
						parts := strings.Split(attachment.MimeType, "/")
						if len(parts) > 1 {
							fileType = parts[1]
						}
					}

					log.Printf("[附件处理] 开始上传附件到OSS，邮件ID: %d, 文件名: %s", emailOne.EmailID, attachment.Filename)
					fmt.Printf("        正在上传到OSS... ")
					var err error
					// 添加重试机制，最多尝试2次
					maxRetries := 2
					for attempt := 1; attempt <= maxRetries; attempt++ {
						log.Printf("[附件处理] 尝试上传附件到OSS (尝试 %d/%d)，邮件ID: %d, 文件名: %s",
							attempt, maxRetries, emailOne.EmailID, attachment.Filename)
						if attempt > 1 {
							fmt.Printf("        重试上传到OSS (尝试 %d/%d)... ", attempt, maxRetries)
						} else {
							fmt.Printf("        正在上传到OSS... ")
						}

						ossURL, err = oss.UploadBase64ToOSS(attachment.Filename, attachment.Base64Data, fileType)
						if err == nil {
							// 上传成功，跳出循环
							log.Printf("[附件处理] 成功上传附件到OSS，邮件ID: %d, 文件名: %s, URL: %s", emailOne.EmailID, attachment.Filename, ossURL)
							fmt.Printf("✅ 成功\n")
							break
						}

						// 上传失败
						if attempt < maxRetries {
							log.Printf("[附件处理] 上传附件到OSS失败，准备重试，邮件ID: %d, 文件名: %s, 错误: %v",
								emailOne.EmailID, attachment.Filename, err)
							fmt.Printf("❌ 失败: %v，准备重试\n", err)
							// 可以在这里添加短暂的延迟
							time.Sleep(time.Second * 2)
						} else {
							// 最后一次尝试也失败了
							log.Printf("[附件处理] 上传附件到OSS失败，已达到最大重试次数，邮件ID: %d, 文件名: %s, 错误: %v",
								emailOne.EmailID, attachment.Filename, err)
							fmt.Printf("❌ 最终失败: %v\n", err)
						}
					}

					// 检查是否所有尝试都失败了
					if err != nil {
						fmt.Printf("[附件处理] 经过 %d 次尝试，上传附件到OSS仍然失败，邮件ID: %d, 文件名: %s\n",
							maxRetries, emailOne.EmailID, attachment.Filename)
					}
				} else {
					log.Printf("[附件处理] 附件没有Base64数据，邮件ID: %d, 文件名: %s", emailOne.EmailID, attachment.Filename)
					fmt.Printf("        附件没有Base64数据，跳过上传\n")
				}

				// 创建附件记录
				attachmentRecord := &model.PrimeEmailContentAttachment{
					EmailID:   emailOne.EmailID,
					AccountId: emailOne.AccountId,
					FileName:  utils.SanitizeUTF8(attachment.Filename),
					SizeKb:    attachment.SizeKB,
					MimeType:  utils.SanitizeUTF8(attachment.MimeType),
					OssUrl:    utils.SanitizeUTF8(ossURL),
					CreatedAt: utils.JsonTime{Time: time.Now()},
					UpdatedAt: utils.JsonTime{Time: time.Now()},
				}

				attachmentRecords = append(attachmentRecords, attachmentRecord)
			}
		} else {
			log.Printf("[邮件处理] 邮件没有附件，邮件ID: %d", emailOne.EmailID)
			fmt.Printf("    📄 邮件没有附件\n")
		}

		// 添加到待处理列表
		allEmailData = append(allEmailData, EmailData{
			EmailID:      emailOne.EmailID,
			AccountId:    emailOne.AccountId,
			EmailContent: emailContent,
			Attachments:  attachmentRecords,
		})
	}

	// 检查处理结果
	fmt.Printf("\n【处理结果】成功: %d, 失败: %d, 总计: %d\n", successCount, failureCount, len(emailIDs))
	log.Printf("[邮件处理] 处理结果 - 成功: %d, 失败: %d, 总计: %d", successCount, failureCount, len(emailIDs))

	// 如果没有成功处理任何邮件，直接返回
	if successCount == 0 {
		log.Printf("[邮件处理] 没有成功处理任何邮件，终止流程")
		fmt.Printf("❌ 没有成功处理任何邮件，终止流程\n")
		return fmt.Errorf("所有 %d 封邮件都处理失败", len(emailIDs))
	}

	// 第二步：将所有数据保存到数据库
	fmt.Printf("\n【第2阶段】将所有数据保存到数据库...\n")

	// 开始数据库事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("[邮件处理] 发生异常，事务回滚: %v", r)
			fmt.Printf("❌ 发生异常，事务回滚: %v\n", r)
		}
	}()

	// 保存邮件内容
	for _, data := range allEmailData {
		// 保存邮件内容
		log.Printf("[邮件处理] 保存邮件内容，ID: %d, 主题: %s", data.EmailID, data.EmailContent.Subject)
		fmt.Printf("  • 保存邮件 ID: %d 内容... ", data.EmailID)

		if err := data.EmailContent.CreateWithTransaction(tx); err != nil {
			log.Printf("[邮件处理] 保存邮件内容失败，ID: %d, 错误: %v", data.EmailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			tx.Rollback()
			return err
		}

		fmt.Printf("✅ 成功\n")

		// 保存附件记录
		if len(data.Attachments) > 0 {
			log.Printf("[邮件处理] 保存 %d 个附件记录，邮件ID: %d", len(data.Attachments), data.EmailID)
			fmt.Printf("    • 保存 %d 个附件记录... ", len(data.Attachments))

			// 使用单个Create而不是批量操作，避免反射问题
			for _, attachment := range data.Attachments {
				if err := tx.Create(attachment).Error; err != nil {
					log.Printf("[附件处理] 保存附件失败: 邮件ID=%d, 文件名=%s, 错误=%v",
						attachment.EmailID, attachment.FileName, err)
					fmt.Printf("❌ 失败: %v\n", err)
					tx.Rollback()
					return err
				}
			}

			fmt.Printf("✅ 成功\n")
		}

		// 更新邮件状态为已处理
		log.Printf("[邮件处理] 更新邮件状态为已处理，邮件ID: %d", data.EmailID)
		fmt.Printf("    • 更新邮件状态为已处理... ")

		if err := tx.Model(&model.PrimeEmail{}).Where("email_id = ?", data.EmailID).Update("status", 1).Error; err != nil {
			log.Printf("[邮件处理] 更新邮件状态失败，邮件ID: %d, 错误: %v", data.EmailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			tx.Rollback()
			return err
		}

		fmt.Printf("✅ 成功\n")
	}

	// 提交事务
	fmt.Printf("\n◉ 提交事务... ")
	if err := tx.Commit().Error; err != nil {
		log.Printf("[邮件处理] 提交事务失败，错误: %v", err)
		fmt.Printf("❌ 失败: %v\n", err)
		tx.Rollback()
		return err
	}

	log.Printf("[邮件处理] 成功提交事务，完成处理 %d 封邮件", len(allEmailData))
	fmt.Printf("✅ 成功\n")

	// 根据处理结果更新账号的同步时间
	fmt.Printf("\n【第3阶段】更新账号同步时间...\n")

	// 统计每个账号的处理结果
	accountResults := make(map[int]struct {
		SuccessCount int
		FailureCount int
	})

	for _, data := range allEmailData {
		result := accountResults[data.AccountId]
		result.SuccessCount++
		accountResults[data.AccountId] = result
	}

	// 对于有处理失败的账号，也需要统计
	for _, emailOne := range emailIDs {
		if _, exists := accountResults[emailOne.AccountId]; !exists {
			// 这个账号的所有邮件都失败了
			result := accountResults[emailOne.AccountId]
			result.FailureCount++
			accountResults[emailOne.AccountId] = result
		}
	}

	// 更新账号的同步时间
	for accountID, result := range accountResults {
		if result.SuccessCount > 0 {
			// 有成功处理的邮件，更新为完成时间
			if err := model.UpdateLastSyncContentTimeOnComplete(accountID); err != nil {
				log.Printf("[邮件处理] 更新账号 %d 完成时间失败: %v", accountID, err)
			} else {
				log.Printf("[邮件处理] 账号 %d 处理完成，更新同步时间", accountID)
				fmt.Printf("  • 账号 %d: 处理完成，更新同步时间\n", accountID)
			}
		} else {
			// 所有邮件都失败了，重置同步时间让其能够被重新优先选择
			if err := model.ResetSyncContentTimeOnFailure(accountID); err != nil {
				log.Printf("[邮件处理] 重置账号 %d 同步时间失败: %v", accountID, err)
			} else {
				log.Printf("[邮件处理] 账号 %d 处理失败，重置同步时间", accountID)
				fmt.Printf("  • 账号 %d: 处理失败，重置同步时间\n", accountID)
			}
		}
	}

	fmt.Printf("========== 邮件处理完成 ==========\n")
	fmt.Printf("成功: %d 封邮件\n", successCount)
	fmt.Printf("失败: %d 封邮件\n", failureCount)
	fmt.Printf("总计: %d 封邮件\n", len(emailIDs))
	fmt.Printf("涉及账号: %d 个\n", len(processedAccounts))
	fmt.Printf("================================\n\n")
	return nil
}

// GetEmailContentWithAccounts 使用预分配的账号获取邮件内容
func GetEmailContentWithAccounts(limit int, node int, accounts []model.PrimeEmailAccount) error {
	if len(accounts) == 0 {
		log.Printf("[邮件处理] 没有分配到账号")
		return nil
	}

	log.Printf("[邮件处理] 节点 %d - 开始处理 %d 个账号的邮件", node, len(accounts))
	fmt.Printf("========== 节点 %d - 开始处理 %d 个账号的邮件 ==========\n", node, len(accounts))

	// 为每个账号获取邮件
	var allEmailIDs []model.PrimeEmail
	perAccountLimit := limit / len(accounts)
	remainder := limit % len(accounts)

	// 记录处理的账号信息
	processedAccounts := make(map[int]string)

	for i, account := range accounts {
		currentLimit := perAccountLimit
		// 将余数分配给前面的账号
		if i < remainder {
			currentLimit++
		}

		if currentLimit == 0 {
			continue
		}

		// 获取该账号的邮件
		accountEmails, err := model.GetEmailByStatusAndAccount(-1, account.ID, currentLimit)
		if err != nil {
			log.Printf("[邮件处理] 获取账号 %d 的邮件失败: %v", account.ID, err)
			continue
		}

		if len(accountEmails) > 0 {
			allEmailIDs = append(allEmailIDs, accountEmails...)
			processedAccounts[account.ID] = account.Account
			log.Printf("[邮件处理] 账号 %d (%s) - 获取到 %d 封待处理邮件", account.ID, account.Account, len(accountEmails))
			fmt.Printf("账号 %d (%s) - 获取到 %d 封待处理邮件\n", account.ID, account.Account, len(accountEmails))
		}
	}

	// 检查是否有邮件需要处理
	if len(allEmailIDs) == 0 {
		log.Printf("[邮件处理] 没有需要处理的新邮件")
		fmt.Println("没有需要处理的新邮件")
		return nil
	}

	emailIDs := allEmailIDs
	folder := "INBOX"

	log.Printf("[邮件处理] 开始处理 %d 封邮件, 文件夹: %s", len(emailIDs), folder)
	fmt.Printf("\n========== 开始处理 %d 封邮件，文件夹: %s ==========\n", len(emailIDs), folder)

	// 存储所有邮件内容和附件，以便后续批量存储
	type EmailData struct {
		EmailID      int
		AccountId    int
		EmailContent *model.PrimeEmailContent
		Attachments  []*model.PrimeEmailContentAttachment
	}

	allEmailData := make([]EmailData, 0, len(emailIDs))

	// 添加计数器
	var successCount, failureCount int

	// 第一步：获取所有邮件内容
	fmt.Printf("\n【第1阶段】获取所有邮件内容...\n")
	for i, emailOne := range emailIDs {
		log.Printf("[邮件处理] 正在获取邮件内容，ID: %d", emailOne.EmailID)
		fmt.Printf("  • 获取邮件 ID: %d 内容... ", emailOne.EmailID)

		// 在处理每个邮件之间添加延迟，避免连接过于频繁
		if i > 0 {
			time.Sleep(time.Millisecond * 500) // 500毫秒延迟
		}

		account, err := model.GetAccountByID(emailOne.AccountId)
		if err != nil && err != gorm.ErrRecordNotFound {
			log.Printf("[邮件处理] 获取邮件账号失败，ID: %d", emailOne.AccountId)
			fmt.Printf("  • 获取邮件账号失败，ID: %d", emailOne.AccountId)
			failureCount++
			continue
		}
		// 为每个请求创建独立的邮件客户端实例
		mailClient, err := newMailClient(account)
		if err != nil {
			log.Printf("[邮件处理] 获取邮箱配置失败: 账号ID=%d, 错误: %v", account.ID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			failureCount++
			// 设置邮件状态为失败
			resetErr := model.ResetEmailStatus(emailOne.EmailID, -2)
			if resetErr != nil {
				log.Printf("[邮件处理] 设置邮件状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
			}
			continue
		}
		email, err := mailClient.GetEmailContent(uint32(emailOne.EmailID), folder)
		if err != nil {
			log.Printf("[邮件处理] 获取邮件内容失败，邮件ID: %d, 错误: %v", emailOne.EmailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			failureCount++

			// 特殊处理：如果是UID不存在的错误，将邮件标记为已删除状态
			if strings.Contains(strings.ToLower(err.Error()), "邮件不存在") ||
				strings.Contains(strings.ToLower(err.Error()), "邮件uid无效") ||
				strings.Contains(strings.ToLower(err.Error()), "bad sequence") {
				log.Printf("[邮件处理] 检测到邮件已删除或UID无效，标记为已删除状态: 邮件ID=%d", emailOne.EmailID)
				resetErr := model.ResetEmailStatus(emailOne.EmailID, -3) // -3表示已删除
				if resetErr != nil {
					log.Printf("[邮件处理] 设置邮件已删除状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
				}
			} else if strings.Contains(strings.ToLower(err.Error()), "server error") ||
				strings.Contains(strings.ToLower(err.Error()), "please try again later") ||
				strings.Contains(strings.ToLower(err.Error()), "service unavailable") ||
				strings.Contains(strings.ToLower(err.Error()), "temporary failure") ||
				strings.Contains(strings.ToLower(err.Error()), "server busy") {
				// SELECT服务器临时错误，将状态回滚为-1以便重新处理
				log.Printf("[邮件处理] 检测到服务器临时错误，回滚状态为待处理: 邮件ID=%d, 错误=%v", emailOne.EmailID, err)
				resetErr := model.ResetEmailStatus(emailOne.EmailID, -1) // -1表示待处理，可以重新尝试
				if resetErr != nil {
					log.Printf("[邮件处理] 回滚邮件状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
				}
			} else {
				// 其他错误，设置为失败状态
				resetErr := model.ResetEmailStatus(emailOne.EmailID, -2)
				if resetErr != nil {
					log.Printf("[邮件处理] 设置邮件状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
				}
			}
			// 继续处理下一个邮件，而不是直接返回错误
			continue
		}

		log.Printf("[邮件处理] 成功获取邮件内容，邮件ID: %d, 主题: %s, 发件人: %s", emailOne.EmailID, email.Subject, email.From)
		fmt.Printf("✅ 成功，主题: %s\n", email.Subject)
		successCount++

		// 创建邮件内容记录
		emailContent := &model.PrimeEmailContent{
			EmailID:       emailOne.EmailID,
			AccountId:     emailOne.AccountId,
			Subject:       utils.SanitizeUTF8(email.Subject),
			FromEmail:     utils.SanitizeUTF8(email.From),
			ToEmail:       utils.SanitizeUTF8(email.To),
			Date:          utils.SanitizeUTF8(email.Date),
			Content:       utils.SanitizeUTF8(email.Body),
			HTMLContent:   utils.SanitizeUTF8(email.BodyHTML),
			Type:          0,
			HasAttachment: emailOne.HasAttachment,
			CreatedAt:     utils.JsonTime{Time: time.Now()},
			UpdatedAt:     utils.JsonTime{Time: time.Now()},
		}

		// 创建附件记录列表
		attachmentRecords := make([]*model.PrimeEmailContentAttachment, 0)
		if len(email.Attachments) > 0 {
			log.Printf("[邮件处理] 邮件含有 %d 个附件，邮件ID: %d", len(email.Attachments), emailOne.EmailID)
			fmt.Printf("    📎 发现 %d 个附件\n", len(email.Attachments))

			// 处理附件
			for i, attachment := range email.Attachments {
				log.Printf("[附件处理] 开始处理附件 %d/%d，邮件ID: %d, 文件名: %s",
					i+1, len(email.Attachments), emailOne.EmailID, attachment.Filename)
				fmt.Printf("      - 附件 %d/%d: %s (%.2f KB, %s)\n",
					i+1, len(email.Attachments), attachment.Filename, attachment.SizeKB, attachment.MimeType)

				// 上传到OSS
				ossURL := ""
				if attachment.Base64Data != "" {
					fileType := ""
					if attachment.MimeType != "" {
						parts := strings.Split(attachment.MimeType, "/")
						if len(parts) > 1 {
							fileType = parts[1]
						}
					}

					log.Printf("[附件处理] 开始上传附件到OSS，邮件ID: %d, 文件名: %s", emailOne.EmailID, attachment.Filename)
					fmt.Printf("        正在上传到OSS... ")
					var err error
					// 添加重试机制，最多尝试2次
					maxRetries := 2
					for attempt := 1; attempt <= maxRetries; attempt++ {
						log.Printf("[附件处理] 尝试上传附件到OSS (尝试 %d/%d)，邮件ID: %d, 文件名: %s",
							attempt, maxRetries, emailOne.EmailID, attachment.Filename)
						if attempt > 1 {
							fmt.Printf("        重试上传到OSS (尝试 %d/%d)... ", attempt, maxRetries)
						} else {
							fmt.Printf("        正在上传到OSS... ")
						}

						ossURL, err = oss.UploadBase64ToOSS(attachment.Filename, attachment.Base64Data, fileType)
						if err == nil {
							// 上传成功，跳出循环
							log.Printf("[附件处理] 成功上传附件到OSS，邮件ID: %d, 文件名: %s, URL: %s", emailOne.EmailID, attachment.Filename, ossURL)
							fmt.Printf("✅ 成功\n")
							break
						}

						// 上传失败
						if attempt < maxRetries {
							log.Printf("[附件处理] 上传附件到OSS失败，准备重试，邮件ID: %d, 文件名: %s, 错误: %v",
								emailOne.EmailID, attachment.Filename, err)
							fmt.Printf("❌ 失败: %v，准备重试\n", err)
							// 可以在这里添加短暂的延迟
							time.Sleep(time.Second * 2)
						} else {
							// 最后一次尝试也失败了
							log.Printf("[附件处理] 上传附件到OSS失败，已达到最大重试次数，邮件ID: %d, 文件名: %s, 错误: %v",
								emailOne.EmailID, attachment.Filename, err)
							fmt.Printf("❌ 最终失败: %v\n", err)
						}
					}

					// 检查是否所有尝试都失败了
					if err != nil {
						fmt.Printf("[附件处理] 经过 %d 次尝试，上传附件到OSS仍然失败，邮件ID: %d, 文件名: %s\n",
							maxRetries, emailOne.EmailID, attachment.Filename)
					}
				} else {
					log.Printf("[附件处理] 附件没有Base64数据，邮件ID: %d, 文件名: %s", emailOne.EmailID, attachment.Filename)
					fmt.Printf("        附件没有Base64数据，跳过上传\n")
				}

				// 创建附件记录
				attachmentRecord := &model.PrimeEmailContentAttachment{
					EmailID:   emailOne.EmailID,
					AccountId: emailOne.AccountId,
					FileName:  utils.SanitizeUTF8(attachment.Filename),
					SizeKb:    attachment.SizeKB,
					MimeType:  utils.SanitizeUTF8(attachment.MimeType),
					OssUrl:    utils.SanitizeUTF8(ossURL),
					CreatedAt: utils.JsonTime{Time: time.Now()},
					UpdatedAt: utils.JsonTime{Time: time.Now()},
				}

				attachmentRecords = append(attachmentRecords, attachmentRecord)
			}
		} else {
			log.Printf("[邮件处理] 邮件没有附件，邮件ID: %d", emailOne.EmailID)
			fmt.Printf("    📄 邮件没有附件\n")
		}

		// 添加到待处理列表
		allEmailData = append(allEmailData, EmailData{
			EmailID:      emailOne.EmailID,
			AccountId:    emailOne.AccountId,
			EmailContent: emailContent,
			Attachments:  attachmentRecords,
		})
	}

	// 检查处理结果
	fmt.Printf("\n【处理结果】成功: %d, 失败: %d, 总计: %d\n", successCount, failureCount, len(emailIDs))
	log.Printf("[邮件处理] 处理结果 - 成功: %d, 失败: %d, 总计: %d", successCount, failureCount, len(emailIDs))

	// 如果没有成功处理任何邮件，直接返回
	if successCount == 0 {
		log.Printf("[邮件处理] 没有成功处理任何邮件，终止流程")
		fmt.Printf("❌ 没有成功处理任何邮件，终止流程\n")
		return fmt.Errorf("所有 %d 封邮件都处理失败", len(emailIDs))
	}

	// 第二步：将所有数据保存到数据库 - 保持原有逻辑
	fmt.Printf("\n【第2阶段】将所有数据保存到数据库...\n")

	// 开始数据库事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("[邮件处理] 发生异常，事务回滚: %v", r)
			fmt.Printf("❌ 发生异常，事务回滚: %v\n", r)
		}
	}()

	// 保存邮件内容
	for _, data := range allEmailData {
		// 保存邮件内容
		log.Printf("[邮件处理] 保存邮件内容，ID: %d, 主题: %s", data.EmailID, data.EmailContent.Subject)
		fmt.Printf("  • 保存邮件 ID: %d 内容... ", data.EmailID)

		if err := data.EmailContent.CreateWithTransaction(tx); err != nil {
			log.Printf("[邮件处理] 保存邮件内容失败，ID: %d, 错误: %v", data.EmailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			tx.Rollback()
			return err
		}

		fmt.Printf("✅ 成功\n")

		// 保存附件记录
		if len(data.Attachments) > 0 {
			log.Printf("[邮件处理] 保存 %d 个附件记录，邮件ID: %d", len(data.Attachments), data.EmailID)
			fmt.Printf("    • 保存 %d 个附件记录... ", len(data.Attachments))

			// 使用单个Create而不是批量操作，避免反射问题
			for _, attachment := range data.Attachments {
				if err := tx.Create(attachment).Error; err != nil {
					log.Printf("[附件处理] 保存附件失败: 邮件ID=%d, 文件名=%s, 错误=%v",
						attachment.EmailID, attachment.FileName, err)
					fmt.Printf("❌ 失败: %v\n", err)
					tx.Rollback()
					return err
				}
			}

			fmt.Printf("✅ 成功\n")
		}

		// 更新邮件状态为已处理
		log.Printf("[邮件处理] 更新邮件状态为已处理，邮件ID: %d", data.EmailID)
		fmt.Printf("    • 更新邮件状态为已处理... ")

		if err := tx.Model(&model.PrimeEmail{}).Where("email_id = ?", data.EmailID).Update("status", 1).Error; err != nil {
			log.Printf("[邮件处理] 更新邮件状态失败，邮件ID: %d, 错误: %v", data.EmailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			tx.Rollback()
			return err
		}

		fmt.Printf("✅ 成功\n")
	}

	// 提交事务
	fmt.Printf("\n◉ 提交事务... ")
	if err := tx.Commit().Error; err != nil {
		log.Printf("[邮件处理] 提交事务失败，错误: %v", err)
		fmt.Printf("❌ 失败: %v\n", err)
		tx.Rollback()
		return err
	}

	log.Printf("[邮件处理] 成功提交事务，完成处理 %d 封邮件", len(allEmailData))
	fmt.Printf("✅ 成功\n")

	// 根据处理结果更新账号的同步时间
	fmt.Printf("\n【第3阶段】更新账号同步时间...\n")

	// 统计每个账号的处理结果
	accountResults := make(map[int]struct {
		SuccessCount int
		FailureCount int
	})

	for _, data := range allEmailData {
		result := accountResults[data.AccountId]
		result.SuccessCount++
		accountResults[data.AccountId] = result
	}

	// 对于有处理失败的账号，也需要统计
	for _, emailOne := range emailIDs {
		if _, exists := accountResults[emailOne.AccountId]; !exists {
			// 这个账号的所有邮件都失败了
			result := accountResults[emailOne.AccountId]
			result.FailureCount++
			accountResults[emailOne.AccountId] = result
		}
	}

	// 更新账号的同步时间
	for accountID, result := range accountResults {
		if result.SuccessCount > 0 {
			// 有成功处理的邮件，更新为完成时间
			if err := model.UpdateLastSyncContentTimeOnComplete(accountID); err != nil {
				log.Printf("[邮件处理] 更新账号 %d 完成时间失败: %v", accountID, err)
			} else {
				log.Printf("[邮件处理] 账号 %d 处理完成，更新同步时间", accountID)
				fmt.Printf("  • 账号 %d: 处理完成，更新同步时间\n", accountID)
			}
		} else {
			// 所有邮件都失败了，重置同步时间让其能够被重新优先选择
			if err := model.ResetSyncContentTimeOnFailure(accountID); err != nil {
				log.Printf("[邮件处理] 重置账号 %d 同步时间失败: %v", accountID, err)
			} else {
				log.Printf("[邮件处理] 账号 %d 处理失败，重置同步时间", accountID)
				fmt.Printf("  • 账号 %d: 处理失败，重置同步时间\n", accountID)
			}
		}
	}

	fmt.Printf("========== 邮件处理完成 ==========\n")
	fmt.Printf("成功: %d 封邮件\n", successCount)
	fmt.Printf("失败: %d 封邮件\n", failureCount)
	fmt.Printf("总计: %d 封邮件\n", len(emailIDs))
	fmt.Printf("涉及账号: %d 个\n", len(processedAccounts))
	fmt.Printf("================================\n\n")
	return nil
}

// 列出邮件附件
func ListAttachments(c *gin.Context) {
	// 为每个请求创建独立的邮件客户端实例
	accounts, err := model.GetActiveAccount()
	if err != nil {
		utils.SendResponse(c, err, "获取邮箱配置失败")
		return
	}
	account := accounts[0]

	// 为每个请求创建独立的邮件客户端实例
	mailClient, err := newMailClient(account)
	if err != nil {
		utils.SendResponse(c, err, "获取邮箱配置失败")
		return
	}
	uidStr := c.Param("uid")
	folder := c.DefaultQuery("folder", "INBOX")

	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		utils.SendResponse(c, err, "无效的UID")
		return
	}

	email, err := mailClient.GetEmailContent(uint32(uid), folder)
	if err != nil {
		utils.SendResponse(c, err, nil)
		return
	}
	utils.SendResponse(c, err, email.Attachments)
}

// ListEmailsRequest 获取邮件列表请求结构
type ListEmailsRequest struct {
	Folder string `json:"folder" binding:"required"`
	Limit  int    `json:"limit" binding:"required"`
}

// ListEmailsByUidRequest 根据UID获取邮件列表请求结构
type ListEmailsByUidRequest struct {
	StartUID  uint64 `json:"start_uid" binding:"required"`
	EndUID    uint64 `json:"end_uid" binding:"required"`
	AccountId int    `json:"account_id" binding:"required"`
}

// GetEmailContentRequest 获取邮件内容请求结构
type GetEmailContentRequest struct {
	Limit int `json:"limit" binding:"required"` // 每个账号同步的邮件数量限制
	Node  int `json:"node" binding:"required"`  // 节点编号，用于筛选特定节点的账号（必填）
}

// SendEmailRequest 发送邮件请求结构体
type SendEmailRequest struct {
	To          string `json:"to"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}

// SyncMultipleAccountsRequest 同步多账号邮件请求结构体
type SyncMultipleAccountsRequest struct {
	MaxWorkers int `json:"max_workers"`             // 最大worker数量
	Limit      int `json:"limit"`                   // 每个账号同步的邮件数量限制
	Node       int `json:"node" binding:"required"` // 节点编号，用于筛选特定节点的账号（必填）
}

//// 发送邮件
//func SendEmail(c *gin.Context) {
//	accounts, err := model.GetActiveAccount()
//	if err != nil {
//		utils.SendResponse(c, err, "获取邮箱配置失败")
//		return
//	}
//	account := accounts[0]
//
//	// 为每个请求创建独立的邮件客户端实例
//	mailClient, err := newMailClient(account)
//	if err != nil {
//		utils.SendResponse(c, err, "获取邮箱配置失败")
//		return
//	}
//	var req SendEmailRequest
//	if err := c.ShouldBindJSON(&req); err != nil {
//		utils.SendResponse(c, err, "无效的参数")
//		return
//	}
//
//	contentType := req.ContentType
//	if contentType != "html" {
//		contentType = "text"
//	}
//
//	err = mailClient.SendEmail(req.To, req.Subject, req.Body, contentType)
//	if err != nil {
//		utils.SendResponse(c, err, nil)
//
//		return
//	}
//	utils.SendResponse(c, err, "邮件发送成功")
//}

func GetForwardOriginalEmail(c *gin.Context) {
	startTime := time.Now() // 开始计时

	// 创建请求结构体
	type ForwardRequest struct {
		EmailID int `json:"email_id"`
		Limit   int `json:"limit"`
		Node    int `json:"node" binding:"required"` // 节点编号，用于筛选特定节点的转发记录（必填）
	}

	var req ForwardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "参数错误")
		return
	}

	// 检查节点参数是否有效
	if req.Node <= 0 {
		utils.SendResponse(c, fmt.Errorf("节点编号必须大于0，当前值: %d", req.Node), "节点编号无效")
		return
	}

	// 如果请求中有email_id，则直接转发该邮件
	if req.EmailID > 0 {
		// 查询这条记录以获取PrimeOp邮箱地址
		var forward model.PrimeEmailForward
		if err := db.DB().First(&forward, "email_id = ?", req.EmailID).Error; err != nil {
			utils.SendResponse(c, err, "未找到对应的转发记录")
			return
		}
		// 获取邮箱配置
		account, err := model.GetAccountByID(forward.AccountId)
		if err != nil {
			utils.SendResponse(c, err, "获取邮箱配置失败")
			return
		}

		// 检查账号是否属于指定节点
		if account.Node != req.Node {
			utils.SendResponse(c, fmt.Errorf("邮件ID %d 属于节点 %d，与请求节点 %d 不匹配", req.EmailID, account.Node, req.Node), "节点不匹配")
			return
		}

		// 为每个请求创建独立的邮件客户端实例
		mailClient, err := newMailClient(account)
		if err != nil {
			utils.SendResponse(c, err, "获取邮箱配置失败")
			return
		}

		// 执行转发
		forwardStartTime := time.Now() // 转发开始时间
		err = mailClient.ForwardStructuredEmail(uint32(req.EmailID), "INBOX", forward.PrimeOp)
		forwardDuration := time.Since(forwardStartTime) // 转发耗时

		if err != nil {
			log.Printf("[邮件转发] 节点 %d - 邮件ID: %d 转发失败, 耗时: %v, 错误: %v", req.Node, req.EmailID, forwardDuration, err)
			utils.SendResponse(c, err, fmt.Sprintf("节点 %d - 转发失败: %v", req.Node, err))
			return
		}

		// 更新状态为已转发(1)
		db.DB().Model(&forward).Update("status", 1)
		totalDuration := time.Since(startTime) // 总耗时
		log.Printf("[邮件转发] 节点 %d - 邮件ID: %d 转发成功, 转发耗时: %v, 总耗时: %v", req.Node, req.EmailID, forwardDuration, totalDuration)
		utils.SendResponse(c, nil, fmt.Sprintf("节点 %d - 邮件转发成功, 耗时: %v", req.Node, forwardDuration))
		return
	}

	// 如果没有指定email_id，则使用封装的函数获取待转发记录
	records, err := model.GetAndUpdatePendingForwardsByNode(req.Limit, req.Node)
	if err != nil {
		utils.SendResponse(c, err, "查询待转发记录失败")
		return
	}

	// 如果没有找到记录
	if len(records) == 0 {
		utils.SendResponse(c, nil, fmt.Sprintf("没有找到节点 %d 的待转发记录", req.Node))
		return
	}

	// 转发邮件
	var successCount, failCount int
	var totalForwardTime time.Duration

	for _, record := range records {
		// 执行转发
		forwardStartTime := time.Now() // 单封邮件转发开始时间
		account, err := model.GetAccountByID(record.AccountId)
		if err != nil {
			utils.SendResponse(c, err, "获取邮箱配置失败")
			return
		}
		mailClient, err := newMailClient(account)
		if err != nil {
			utils.SendResponse(c, err, "获取邮箱配置失败")
			return
		}
		err = mailClient.ForwardStructuredEmail(uint32(record.EmailID), "INBOX", record.PrimeOp)
		forwardDuration := time.Since(forwardStartTime) // 单封邮件转发耗时
		totalForwardTime += forwardDuration

		if err != nil {
			failCount++
			// 使用封装的函数更新失败状态
			if updateErr := model.UpdateForwardFailureStatus(record.ID, err); updateErr != nil {
				log.Printf("[邮件转发] 更新失败状态失败: %v", updateErr)
			}
			log.Printf("[邮件转发] 节点 %d - 邮件ID: %d 转发失败, 耗时: %v, 错误: %v", req.Node, record.EmailID, forwardDuration, err)
		} else {
			successCount++
			// 使用封装的函数更新成功状态
			if updateErr := model.UpdateForwardSuccessStatus(record.ID); updateErr != nil {
				log.Printf("[邮件转发] 更新成功状态失败: %v", updateErr)
			}
			log.Printf("[邮件转发] 节点 %d - 邮件ID: %d 转发成功, 耗时: %v", req.Node, record.EmailID, forwardDuration)

		}
	}

	totalDuration := time.Since(startTime)
	avgTime := time.Duration(0)
	if len(records) > 0 {
		avgTime = totalForwardTime / time.Duration(len(records))
	}

	result := map[string]interface{}{
		"节点":     req.Node,
		"总耗时":    totalDuration.String(),
		"平均转发耗时": avgTime.String(),
		"成功数":    successCount,
		"失败数":    failCount,
	}

	log.Printf("[邮件转发] 节点 %d - 批量转发完成: 成功 %d 条, 失败 %d 条, 总耗时: %v, 平均耗时: %v",
		req.Node, successCount, failCount, totalDuration, avgTime)

	utils.SendResponse(c, nil, result)
}

// SyncEmails 定时同步邮件的函数，不依赖gin.Context
func SyncEmails() {
	log.Printf("开始定时同步邮件")

	accounts, err := model.GetActiveAccount()
	if err != nil {
		log.Printf("获取邮箱配置失败: %v", err)
		return
	}
	account := accounts[0]

	// 为每个请求创建独立的邮件客户端实例
	mailClient, err := newMailClient(account)
	if err != nil {
		log.Printf("创建邮件客户端失败: %v", err)
		return
	}

	// 默认参数
	folder := "INBOX"
	limit := 50 // 设置一个合理的默认值

	// 使用数据库事务获取最新邮件ID并处理邮件
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("同步邮件时发生异常: %v", r)
		}
	}()

	lastEmail, err := model.GetLatestEmailWithTx(tx, account.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果没有记录，设置最大ID为0
			log.Printf("数据库中没有邮件记录，可能为第一次同步")
		} else {
			// 其他错误
			tx.Rollback()
			log.Printf("获取最大email_id失败: %v", err)
			return
		}
	}

	var emailsResult []mailclient.EmailInfo
	if lastEmail.EmailID > 0 {
		log.Printf("当前数据库最大email_id: %d", lastEmail.EmailID)
		// 【修复】使用SEARCH命令搜索大于指定UID的邮件，而不是使用连续的UID范围
		// 这样可以避免UID不连续导致的问题
		emailsResult, err = mailClient.ListEmailsFromUID(folder, limit, uint32(lastEmail.EmailID))
	} else {
		// 获取最新邮件（原有功能）
		emailsResult, err = mailClient.ListEmails(folder, limit)
	}

	if err != nil {
		tx.Rollback()
		log.Printf("获取邮件列表失败: %v", err)
		return
	}

	var emailList []*model.PrimeEmail
	for _, email := range emailsResult {
		var emailInfo model.PrimeEmail
		emailInfo.EmailID, _ = strconv.Atoi(email.EmailID)
		emailInfo.FromEmail = utils.SanitizeUTF8(email.From)
		emailInfo.Subject = utils.SanitizeUTF8(email.Subject)
		emailInfo.Date = utils.SanitizeUTF8(email.Date)
		emailInfo.HasAttachment = 0
		emailInfo.AccountId = account.ID
		emailInfo.Status = -1
		if email.HasAttachments == true {
			emailInfo.HasAttachment = 1
		}
		emailInfo.CreatedAt = utils.JsonTime{Time: time.Now()}

		emailList = append(emailList, &emailInfo)
	}

	if len(emailList) > 0 {
		// 使用容错批量插入
		result, err := model.BatchCreateEmailsWithStats(emailList, tx)
		if err != nil {
			tx.Rollback()
			log.Printf("批量创建邮件记录失败: %v", err)
			return
		}

		// 记录批量插入结果
		log.Printf("SyncEmails - 批量插入结果: 总计:%d, 成功:%d, 跳过:%d, 失败:%d",
			result.TotalCount, result.SuccessCount, result.SkippedCount, result.FailedCount)

		// 更新账号的最后同步时间
		if err := model.UpdateLastSyncTimeWithTx(tx, account.ID); err != nil {
			tx.Rollback()
			log.Printf("SyncEmails - 账号ID %d: 更新最后同步时间失败: %v", account.ID, err)
			return
		}

		if err := tx.Commit().Error; err != nil {
			log.Printf("提交事务失败: %v", err)
			return
		}

		log.Printf("成功同步 %d 封新邮件，已更新最后同步时间", len(emailList))
	} else {
		// 即使没有新邮件，也要更新最后同步时间
		if err := model.UpdateLastSyncTimeWithTx(tx, account.ID); err != nil {
			tx.Rollback()
			log.Printf("SyncEmails - 账号ID %d: 更新最后同步时间失败: %v", account.ID, err)
			return
		}

		if err := tx.Commit().Error; err != nil {
			log.Printf("提交事务失败: %v", err)
			return
		}

		log.Printf("没有新邮件需要同步，但已更新最后同步时间")
	}
}

// SyncMultipleAccounts 处理多个账号的邮件同步，限制最大并发数
func SyncMultipleAccounts(c *gin.Context) {
	var req SyncMultipleAccountsRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
		return
	}

	// 检查节点参数是否有效
	if req.Node <= 0 {
		utils.SendResponse(c, fmt.Errorf("节点编号必须大于0，当前值: %d", req.Node), "节点编号无效")
		return
	}

	// 使用互斥锁保护并发访问
	emailListProcessMutex.Lock()

	// 检查是否已达到最大全局协程数
	if atomic.LoadInt32(&currentEmailListGoroutines) >= maxEmailListTotalGoroutines {
		emailListProcessMutex.Unlock()
		utils.SendResponse(c, nil, "已达到全局最大处理协程数量，请等待当前任务完成")
		return
	}

	// 计算本次请求可以创建的协程数量
	remainingSlots := maxEmailListTotalGoroutines - atomic.LoadInt32(&currentEmailListGoroutines)

	// 设置默认值
	maxWorkers := req.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 1 // 默认最大worker数量为1
	}

	// 确保不超过剩余的全局协程槽位
	if int32(maxWorkers) > remainingSlots {
		maxWorkers = int(remainingSlots)
	}

	// 原子性地获取账号并立即更新状态，防止并发竞争
	// 先尝试获取足够的账号（为每个worker预留账号）
	filteredAccounts, err := model.GetAndUpdateAccountsForList(req.Node, maxWorkers*5)
	if err != nil {
		emailListProcessMutex.Unlock()
		utils.SendResponse(c, err, "获取邮箱配置失败")
		return
	}

	if len(filteredAccounts) == 0 {
		emailListProcessMutex.Unlock()
		utils.SendResponse(c, nil, fmt.Sprintf("没有找到节点 %d 的可用邮箱账号（可能都在处理中）", req.Node))
		return
	}

	// 根据实际获取到的账号数量调整worker数量
	if len(filteredAccounts) < maxWorkers {
		maxWorkers = len(filteredAccounts)
	}

	// 如果没有可用的协程槽位
	if maxWorkers <= 0 {
		emailListProcessMutex.Unlock()
		utils.SendResponse(c, nil, "无法创建工作协程，请等待当前任务完成")
		return
	}

	// 更新全局协程计数
	atomic.AddInt32(&currentEmailListGoroutines, int32(maxWorkers))

	log.Printf("[邮件同步] 节点 %d - 原子性获取并更新了 %d 个账号的状态，将创建 %d 个工作协程", req.Node, len(filteredAccounts), maxWorkers)

	nodeInfo := fmt.Sprintf("节点 %d ", req.Node)

	log.Printf("[邮件同步] %s当前全局协程数: %d, 本次请求将创建 %d 个工作协程处理 %d 个账号",
		nodeInfo, atomic.LoadInt32(&currentEmailListGoroutines), maxWorkers, len(filteredAccounts))
	fmt.Printf("[邮件同步] %s当前全局协程数: %d, 本次请求将创建 %d 个工作协程处理 %d 个账号\n",
		nodeInfo, atomic.LoadInt32(&currentEmailListGoroutines), maxWorkers, len(filteredAccounts))

	emailListProcessMutex.Unlock()

	limit := req.Limit
	if limit <= 0 {
		limit = 50 // 默认每个账号同步的邮件数量
	}

	// 创建任务通道
	tasks := make(chan model.PrimeEmailAccount, len(filteredAccounts))

	// 创建结果通道
	results := make(chan struct {
		AccountID int
		Error     error
		Count     int
	}, len(filteredAccounts))

	// 启动工作池
	var wg sync.WaitGroup

	log.Printf("[邮件同步] 启动 %d 个工作协程处理 %d 个账号", maxWorkers, len(filteredAccounts))
	fmt.Printf("[邮件同步] 启动 %d 个工作协程处理 %d 个账号\n", maxWorkers, len(filteredAccounts))

	// 启动worker goroutines
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			defer func() {
				// 完成时减少全局计数
				atomic.AddInt32(&currentEmailListGoroutines, -1)
				log.Printf("[邮件同步] 工作协程 %d 完成，剩余全局协程数: %d",
					workerID, atomic.LoadInt32(&currentEmailListGoroutines))
				fmt.Printf("[邮件同步] 工作协程 %d 完成，剩余全局协程数: %d\n",
					workerID, atomic.LoadInt32(&currentEmailListGoroutines))

				// 捕获panic，防止worker崩溃导致计数不准确
				if r := recover(); r != nil {
					log.Printf("[邮件同步] 工作协程 %d 发生panic: %v", workerID, r)
					fmt.Printf("[邮件同步] 工作协程 %d 发生panic: %v\n", workerID, r)
				}
			}()

			for account := range tasks {
				log.Printf("[邮件同步] 工作协程 %d 开始处理账号: %s", workerID, account.Account)
				fmt.Printf("[邮件同步] 工作协程 %d 开始处理账号: %s\n", workerID, account.Account)

				count, err := syncSingleAccount(account, limit)

				// 根据处理结果更新账号状态
				if err != nil {
					// 处理失败，重置同步时间让其能被重新优先选择
					if updateErr := model.ResetSyncListTimeOnFailure(account.ID); updateErr != nil {
						log.Printf("[邮件同步] 重置账号 %d 状态失败: %v", account.ID, updateErr)
					} else {
						log.Printf("[邮件同步] 账号 %d 处理失败，已重置状态", account.ID)
					}
				} else {
					// 处理成功，更新为完成时间
					if updateErr := model.UpdateLastSyncListTimeOnComplete(account.ID); updateErr != nil {
						log.Printf("[邮件同步] 更新账号 %d 完成状态失败: %v", account.ID, updateErr)
					} else {
						log.Printf("[邮件同步] 账号 %d 处理完成，已更新状态", account.ID)
					}
				}

				results <- struct {
					AccountID int
					Error     error
					Count     int
				}{
					AccountID: account.ID,
					Error:     err,
					Count:     count,
				}
				log.Printf("[邮件同步] 工作协程 %d 完成账号: %s 处理", workerID, account.Account)
				fmt.Printf("[邮件同步] 工作协程 %d 完成账号: %s 处理\n", workerID, account.Account)

			}
		}(i + 1)
	}

	// 发送所有任务
	go func() {
		for _, account := range filteredAccounts {
			tasks <- account
		}
		close(tasks) // 关闭任务通道，表示没有更多任务
	}()

	// 收集结果的goroutine
	go func() {
		wg.Wait()      // 等待所有worker完成
		close(results) // 关闭结果通道
	}()

	// 构造返回消息
	responseMsg := fmt.Sprintf("正在同步节点 %d 的 %d 个邮箱账号，使用 %d 个工作协程，当前全局协程数: %d",
		req.Node, len(filteredAccounts), maxWorkers, atomic.LoadInt32(&currentEmailListGoroutines))

	// 返回正在处理的信息
	utils.SendResponse(c, nil, responseMsg)

	// 后台处理结果
	go func() {
		successCount := 0
		failCount := 0
		resultMap := make(map[int]string)

		for result := range results {
			if result.Error != nil {
				failCount++
				resultMap[result.AccountID] = fmt.Sprintf("同步失败: %v", result.Error)
				log.Printf("[邮件同步] 账号ID %d 同步失败: %v", result.AccountID, result.Error)
				fmt.Printf("[邮件同步] 账号ID %d 同步失败: %v\n", result.AccountID, result.Error)

			} else {
				successCount++
				resultMap[result.AccountID] = fmt.Sprintf("同步成功, 获取了 %d 封邮件", result.Count)
				log.Printf("[邮件同步] 账号ID %d 同步成功, 获取了 %d 封邮件", result.AccountID, result.Count)
				fmt.Printf("[邮件同步] 账号ID %d 同步成功, 获取了 %d 封邮件\n", result.AccountID, result.Count)

			}
		}

		log.Printf("[邮件同步] 所有账号同步完成: 成功 %d 个, 失败 %d 个", successCount, failCount)
		fmt.Printf("[邮件同步] 所有账号同步完成: 成功 %d 个, 失败 %d 个\n", successCount, failCount)

	}()
}

// GetConnectionPoolStatus 获取连接池状态
func GetConnectionPoolStatus(c *gin.Context) {
	stats := mailclient.GetConnectionPoolStats()
	utils.SendResponse(c, nil, stats)
}

// syncSingleAccount 同步单个账号的邮件
func syncSingleAccount(account model.PrimeEmailAccount, limit int) (int, error) {
	// 为每个账号创建独立的邮件客户端实例
	mailClient, err := newMailClient(account)
	if err != nil {
		return 0, fmt.Errorf("创建邮件客户端失败: %v", err)
	}

	// 默认参数
	folder := "INBOX"

	// 使用数据库事务获取最新邮件ID并处理邮件
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("同步邮件时发生异常: %v", r)
			fmt.Printf("同步邮件时发生异常: %v\n", r)
		}
	}()

	lastEmail, err := model.GetLatestEmailWithTx(tx, account.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果没有记录，设置最大ID为0
			log.Printf("账号ID %d 数据库中没有邮件记录，可能为第一次同步", account.ID)
			fmt.Printf("账号ID %d 数据库中没有邮件记录，可能为第一次同步\n", account.ID)

		} else {
			// 其他错误
			tx.Rollback()
			return 0, fmt.Errorf("获取最大email_id失败: %v", err)
		}
	}

	var emailsResult []mailclient.EmailInfo
	if lastEmail.EmailID > 0 {
		log.Printf("账号ID %d 当前数据库最大email_id: %d", account.ID, lastEmail.EmailID)
		fmt.Printf("账号ID %d 当前数据库最大email_id: %d\n", account.ID, lastEmail.EmailID)

		// 【修复】使用SEARCH命令搜索大于指定UID的邮件，而不是使用连续的UID范围
		// 这样可以避免UID不连续导致的问题
		emailsResult, err = mailClient.ListEmailsFromUID(folder, limit, uint32(lastEmail.EmailID))
	} else {
		// 获取最新邮件（原有功能）
		emailsResult, err = mailClient.ListEmails(folder, limit)
	}

	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("获取邮件列表失败: %v", err)
	}

	// 如果没有新邮件，更新同步时间后提交事务并返回
	if len(emailsResult) == 0 {
		// 即使没有新邮件，也要更新最后同步时间
		if err := model.UpdateLastSyncTimeWithTx(tx, account.ID); err != nil {
			tx.Rollback()
			log.Printf("账号ID %d: 更新最后同步时间失败: %v", account.ID, err)
			return 0, fmt.Errorf("更新最后同步时间失败: %v", err)
		}

		log.Printf("账号ID %d: 没有新邮件，但已更新最后同步时间", account.ID)
		fmt.Printf("账号ID %d: 没有新邮件，但已更新最后同步时间\n", account.ID)

		if err := tx.Commit().Error; err != nil {
			return 0, fmt.Errorf("提交事务失败: %v", err)
		}
		return 0, nil
	}

	// 构建邮件列表
	var emailList []*model.PrimeEmail
	for _, email := range emailsResult {
		emailID, _ := strconv.Atoi(email.EmailID)
		emailInfo := &model.PrimeEmail{
			EmailID:       emailID,
			FromEmail:     utils.SanitizeUTF8(email.From),
			Subject:       utils.SanitizeUTF8(email.Subject),
			Date:          utils.SanitizeUTF8(email.Date),
			HasAttachment: 0,
			AccountId:     account.ID,
			Status:        -1, // 初始状态
			CreatedAt:     utils.JsonTime{Time: time.Now()},
		}

		if email.HasAttachments {
			emailInfo.HasAttachment = 1
		}

		emailList = append(emailList, emailInfo)
	}

	// 批量创建邮件记录（容错处理）
	result, err := model.BatchCreateEmailsWithStats(emailList, tx)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("批量创建邮件记录失败: %v", err)
	}

	// 记录批量插入的结果
	log.Printf("账号ID %d: 批量插入结果 - 总计:%d, 成功:%d, 跳过:%d, 失败:%d",
		account.ID, result.TotalCount, result.SuccessCount, result.SkippedCount, result.FailedCount)
	fmt.Printf("账号ID %d: 批量插入结果 - 总计:%d, 成功:%d, 跳过:%d, 失败:%d\n",
		account.ID, result.TotalCount, result.SuccessCount, result.SkippedCount, result.FailedCount)

	// 更新账号的最后同步时间
	if err := model.UpdateLastSyncTimeWithTx(tx, account.ID); err != nil {
		tx.Rollback()
		log.Printf("账号ID %d: 更新最后同步时间失败: %v", account.ID, err)
		return 0, fmt.Errorf("更新最后同步时间失败: %v", err)
	}

	log.Printf("账号ID %d: 更新最后同步时间成功", account.ID)
	fmt.Printf("账号ID %d: 更新最后同步时间成功\n", account.ID)

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return 0, fmt.Errorf("提交事务失败: %v", err)
	}

	return result.SuccessCount, nil
}

func ListEmailsByUid(c *gin.Context) {
	// 使用互斥锁确保同一时间只有一个请求在处理邮件列表
	listEmailsByUidMutex.Lock()
	defer listEmailsByUidMutex.Unlock()

	var req ListEmailsByUidRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
		return
	}
	account, err := model.GetAccountByID(req.AccountId)
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Printf("获取邮件账号失败，ID: %d", account.ID)
		fmt.Printf("获取邮件账号失败，ID: %d", account.ID)
	}
	// 为每个请求创建独立的邮件客户端实例
	mailClient, err := newMailClient(account)
	if err != nil {
		utils.SendResponse(c, err, "获取邮箱配置失败")
		return
	}
	// 使用数据库事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	var emailsResult []mailclient.EmailInfo
	startUID := req.StartUID
	endUID := req.EndUID

	count := int(endUID - startUID)
	// 使用UID范围获取邮件
	emailsResult, err = mailClient.ListEmails("INBOX", count, uint32(startUID), uint32(endUID))

	if err != nil {
		tx.Rollback()
		utils.SendResponse(c, err, nil)
		return
	}

	var emailList []*model.PrimeEmail
	for _, email := range emailsResult {
		var emailInfo model.PrimeEmail
		emailInfo.EmailID, _ = strconv.Atoi(email.EmailID)
		emailInfo.FromEmail = utils.SanitizeUTF8(email.From)
		emailInfo.Subject = utils.SanitizeUTF8(email.Subject)
		emailInfo.Date = utils.SanitizeUTF8(email.Date)
		emailInfo.HasAttachment = 0
		emailInfo.Status = -1
		emailInfo.AccountId = account.ID
		if email.HasAttachments == true {
			emailInfo.HasAttachment = 1
		}
		emailInfo.CreatedAt = utils.JsonTime{Time: time.Now()}

		emailList = append(emailList, &emailInfo)
	}

	// 使用容错批量插入
	result, err := model.BatchCreateEmailsWithStats(emailList, tx)
	if err != nil {
		tx.Rollback()
		utils.SendResponse(c, err, nil)
		return
	}

	// 记录批量插入结果
	log.Printf("ListEmailsByUid - 批量插入结果: 总计:%d, 成功:%d, 跳过:%d, 失败:%d",
		result.TotalCount, result.SuccessCount, result.SkippedCount, result.FailedCount)

	if err := tx.Commit().Error; err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	utils.SendResponse(c, nil, emailsResult)
}
