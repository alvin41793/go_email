package api

import (
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

	// 【关键修复】检查是否有邮件需要处理，如果没有则重置所有账号状态
	if len(allEmailIDs) == 0 {
		log.Printf("[邮件处理] 没有需要处理的新邮件，重置所有账号状态")
		fmt.Println("没有需要处理的新邮件，重置所有账号状态")

		// 重置所有账号的状态，避免卡死
		for _, account := range accounts {
			if err := model.UpdateLastSyncContentTimeOnComplete(account.ID); err != nil {
				log.Printf("[邮件处理] 重置账号 %d 状态失败: %v", account.ID, err)
			} else {
				log.Printf("[邮件处理] 账号 %d (%s) 状态已重置", account.ID, account.Account)
				fmt.Printf("  • 账号 %d (%s): 状态已重置\n", account.ID, account.Account)
			}
		}
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

	// 【关键修复】检查是否有邮件需要处理，如果没有则重置所有账号状态
	if len(allEmailIDs) == 0 {
		log.Printf("[邮件处理] 没有需要处理的新邮件，重置所有账号状态")
		fmt.Println("没有需要处理的新邮件，重置所有账号状态")

		// 重置所有账号的状态，避免卡死
		for _, account := range accounts {
			if err := model.UpdateLastSyncContentTimeOnComplete(account.ID); err != nil {
				log.Printf("[邮件处理] 重置账号 %d 状态失败: %v", account.ID, err)
			} else {
				log.Printf("[邮件处理] 账号 %d (%s) 状态已重置", account.ID, account.Account)
				fmt.Printf("  • 账号 %d (%s): 状态已重置\n", account.ID, account.Account)
			}
		}
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

// ListEmailsByUidRequest 根据UID获取邮件列表请求结构
type ListEmailsByUidRequest struct {
	StartUID  uint64 `json:"start_uid" binding:"required"`
	EndUID    uint64 `json:"end_uid" binding:"required"`
	AccountId int    `json:"account_id" binding:"required"`
}

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

// GetGoroutineStats 获取协程统计信息
func GetGoroutineStats(c *gin.Context) {
	stats := utils.GlobalSafeGoroutineManager.GetGoroutineStats()
	utils.SendResponse(c, nil, stats)
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

// CleanupStuckAccounts 清理卡死的账号状态
func CleanupStuckAccounts(c *gin.Context) {
	// 获取参数，默认清理超过50分钟还在处理中的账号
	timeoutMinutes := 50
	if timeoutStr := c.Query("timeout_minutes"); timeoutStr != "" {
		if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
			timeoutMinutes = t
		}
	}

	// 只清理指定节点的账号（可选）
	node := 2
	if nodeStr := c.Query("node"); nodeStr != "" {
		if n, err := strconv.Atoi(nodeStr); err == nil && n > 0 {
			node = n
		}
	}

	cleaned, err := model.CleanupStuckProcessingAccounts(timeoutMinutes, node)
	if err != nil {
		log.Printf("[状态清理] 清理卡死账号失败: %v", err)
		utils.SendResponse(c, err, "清理失败")
		return
	}

	message := fmt.Sprintf("成功清理 %d 个卡死账号状态（超过 %d 分钟）", cleaned, timeoutMinutes)
	if node > 0 {
		message = fmt.Sprintf("成功清理节点 %d 的 %d 个卡死账号状态（超过 %d 分钟）", node, cleaned, timeoutMinutes)
	}

	log.Printf("[状态清理] %s", message)
	utils.SendResponse(c, nil, message)
}
