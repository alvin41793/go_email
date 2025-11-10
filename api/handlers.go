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

	"runtime"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// uploadWithRetry 带重试机制的OSS上传函数
func uploadWithRetry(filename, base64Data, fileType string, emailID int, logContext string) (string, error) {
	maxRetries := 3
	var err error
	var ossURL string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ossStartTime := time.Now()
		log.Printf("[%s] 尝试上传文件到OSS (尝试 %d/%d)，邮件ID: %d, 文件名: %s",
			logContext, attempt, maxRetries, emailID, filename)

		// 使用完整包路径调用OSS上传
		ossURL, err = oss.UploadBase64ToOSS(filename, base64Data, fileType)
		ossDuration := time.Since(ossStartTime)

		if err == nil {
			// 上传成功，跳出循环
			log.Printf("[%s] 成功上传文件到OSS，邮件ID: %d, 文件名: %s, 耗时: %v, URL: %s",
				logContext, emailID, filename, ossDuration, ossURL)
			return ossURL, nil
		}

		// 上传失败
		if attempt < maxRetries {
			log.Printf("[%s] 上传文件到OSS失败，准备重试，邮件ID: %d, 文件名: %s, 耗时: %v, 错误: %v",
				logContext, emailID, filename, ossDuration, err)
			// 添加短暂的延迟
			time.Sleep(time.Second * 2)
		} else {
			// 最后一次尝试也失败了
			log.Printf("[%s] 上传文件到OSS失败，已达到最大重试次数，邮件ID: %d, 文件名: %s, 总耗时: %v, 错误: %v",
				logContext, emailID, filename, ossDuration, err)
		}
	}

	// 尝试备用上传方法
	log.Printf("[%s] 经过 %d 次尝试，上传文件到OSS仍然失败，尝试使用阿里云OSS备用上传，邮件ID: %d, 文件名: %s",
		logContext, maxRetries, emailID, filename)

	ossUploader, fallbackErr := oss.NewOSSUploader()
	if fallbackErr != nil {
		log.Printf("[%s] 创建阿里云OSS上传器失败，邮件ID: %d, 文件名: %s, 错误: %v",
			logContext, emailID, filename, fallbackErr)
		return "", fmt.Errorf("主上传失败: %v, 备用上传器创建失败: %v", err, fallbackErr)
	}

	fallbackURL, _, fallbackErr := ossUploader.UploadFileFromBase64(base64Data, filename, "email_attachments")
	if fallbackErr != nil {
		log.Printf("[%s] 阿里云OSS备用上传也失败，邮件ID: %d, 文件名: %s, 错误: %v",
			logContext, emailID, filename, fallbackErr)
		return "", fmt.Errorf("主上传失败: %v, 备用上传失败: %v", err, fallbackErr)
	}

	log.Printf("[%s] 阿里云OSS备用上传成功，邮件ID: %d, 文件名: %s, URL: %s",
		logContext, emailID, filename, fallbackURL)
	return fallbackURL, nil
}

// handleEmailError 统一处理邮件错误并设置相应状态
func handleEmailError(emailID int, err error, logContext string) int {
	errStr := strings.ToLower(err.Error())
	var newStatus int

	// 检查是否是邮件已删除或UID无效的错误
	if strings.Contains(errStr, "邮件不存在") ||
		strings.Contains(errStr, "邮件uid无效") ||
		strings.Contains(errStr, "bad sequence") {
		newStatus = -3 // 已删除
		log.Printf("[%s] 检测到邮件已删除或UID无效，标记为已删除状态: 邮件ID=%d", logContext, emailID)
	} else if strings.Contains(errStr, "server error") ||
		strings.Contains(errStr, "please try again later") ||
		strings.Contains(errStr, "service unavailable") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "server busy") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "read tcp") ||
		strings.Contains(errStr, "write tcp") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "operation timed out") ||
		strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "context canceled") ||
		strings.Contains(errStr, "error reading response") ||
		strings.Contains(errStr, "连接状态异常") ||
		strings.Contains(errStr, "需要重新建立连接") {
		newStatus = -1 // 临时错误，重新处理
		log.Printf("[%s] 检测到临时错误，回滚状态为待处理: 邮件ID=%d, 错误=%v", logContext, emailID, err)
	} else {
		newStatus = -2 // 永久失败
		log.Printf("[%s] 其他错误，设置为失败状态: 邮件ID=%d, 错误=%v", logContext, emailID, err)
	}

	// 更新邮件状态
	if resetErr := model.ResetEmailStatus(emailID, newStatus); resetErr != nil {
		log.Printf("[%s] 设置邮件状态失败，邮件ID: %d, 状态: %d, 错误: %v", logContext, emailID, newStatus, resetErr)
	}

	return newStatus
}

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
	mailConfig.Password = password
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

			// 使用统一错误处理函数
			handleEmailError(emailOne.EmailID, err, "邮件处理")
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
					// 使用统一的上传重试函数
					var err error
					ossURL, err = uploadWithRetry(attachment.Filename, attachment.Base64Data, fileType, emailOne.EmailID, "附件处理")
					if err == nil {
						fmt.Printf("✅ 成功\n")
					} else {
						fmt.Printf("❌ 最终失败: %v\n", err)
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

			// 使用统一错误处理函数
			handleEmailError(emailOne.EmailID, err, "邮件处理")
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
					// 使用统一的上传重试函数
					var err error
					ossURL, err = uploadWithRetry(attachment.Filename, attachment.Base64Data, fileType, emailOne.EmailID, "附件处理")
					if err == nil {
						fmt.Printf("✅ 成功\n")
					} else {
						fmt.Printf("❌ 最终失败: %v\n", err)
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
	EmailID   int `json:"email_id" binding:"required"`   // 用于获取详情的邮件ID
	AccountId int `json:"account_id" binding:"required"` // 邮箱账号ID
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

	// 获取账号信息
	account, err := model.GetAccountByID(req.AccountId)
	if err != nil {
		log.Printf("获取邮件账号失败，ID: %d, 错误: %v", req.AccountId, err)
		utils.SendResponse(c, err, "获取邮箱账号失败")
		return
	}

	// 为请求创建独立的邮件客户端实例
	mailClient, err := newMailClient(account)
	if err != nil {
		utils.SendResponse(c, err, "获取邮箱配置失败")
		return
	}

	// 结果结构体
	type TestResult struct {
		Account struct {
			ID      int    `json:"id"`
			Account string `json:"account"`
		} `json:"account"`
		EmailList   []mailclient.EmailInfo `json:"email_list"`
		EmailDetail *mailclient.Email      `json:"email_detail"`
	}

	result := TestResult{}
	result.Account.ID = account.ID
	result.Account.Account = account.Account

	// 第一步：获取邮件列表（获取包含给定email_id在内的5封邮件）
	folder := "INBOX"
	log.Printf("[测试接口] 获取邮件列表，账号ID: %d, 邮件ID: %d", account.ID, req.EmailID)

	// 从略小于传入email_id的值开始获取，确保包含传入的email_id
	startID := uint32(req.EmailID)
	if startID > 1 {
		startID = startID - 1 // 从前一个ID开始，确保包含当前ID
	}

	// 获取从startID开始的5封邮件
	emailsResult, err := mailClient.ListEmailsFromUID(folder, 5, startID)
	if err != nil {
		utils.SendResponse(c, err, "获取邮件列表失败")
		return
	}

	result.EmailList = emailsResult
	log.Printf("[测试接口] 成功获取 %d 封邮件列表", len(emailsResult))

	// 第二步：获取指定email_id的邮件详情
	log.Printf("[测试接口] 获取邮件详情，邮件ID: %d", req.EmailID)

	// 先查询PrimeEmail表中的HasAttachment值
	var primeEmail model.PrimeEmail
	skipAttachments := false
	if err := db.DB().Where("email_id = ? AND account_id = ?", req.EmailID, account.ID).First(&primeEmail).Error; err == nil {
		// 如果查询成功且HasAttachment为0，则跳过附件解析
		if primeEmail.HasAttachment == 0 {
			skipAttachments = true
			log.Printf("[测试接口] PrimeEmail表显示邮件无附件，将跳过附件解析，邮件ID: %d", req.EmailID)
		}
	}

	email, err := mailClient.GetEmailContent(uint32(req.EmailID), folder, skipAttachments)
	if err != nil {
		log.Printf("[测试接口] 获取邮件详情失败: %v", err)
		// 即使获取详情失败，也返回已获取的列表信息
		utils.SendResponse(c, err, result)
		return
	}

	result.EmailDetail = email
	log.Printf("[测试接口] 成功获取邮件详情，邮件ID: %d", req.EmailID)

	// 返回结果
	utils.SendResponse(c, nil, result)
}

// GetGoroutineStats 获取协程统计信息
func GetGoroutineStats(c *gin.Context) {
	stats := utils.GlobalSafeGoroutineManager.GetGoroutineStats()

	// 添加当前邮件同步协程数
	stats.UnifiedSyncGoroutines = atomic.LoadInt32(&currentUnifiedSyncs)

	// 检查是否有异常情况
	warnings := make([]string, 0)

	if stats.SystemGoroutines > 300 {
		warnings = append(warnings, fmt.Sprintf("系统协程数过多: %d", stats.SystemGoroutines))
	}

	if stats.ManagedGoroutines > stats.MaxGoroutines*80/100 {
		warnings = append(warnings, fmt.Sprintf("管理协程数接近上限: %d/%d", stats.ManagedGoroutines, stats.MaxGoroutines))
	}

	if len(stats.LongRunning) > 5 {
		warnings = append(warnings, fmt.Sprintf("长时间运行协程过多: %d", len(stats.LongRunning)))
	}

	// 检查邮件同步协程是否卡死
	if stats.UnifiedSyncGoroutines > maxUnifiedSyncs*80/100 {
		warnings = append(warnings, fmt.Sprintf("邮件同步协程数接近上限: %d/%d", stats.UnifiedSyncGoroutines, maxUnifiedSyncs))
	}

	// 添加警告信息
	response := map[string]interface{}{
		"stats":    stats,
		"warnings": warnings,
		"status":   "healthy",
	}

	if len(warnings) > 0 {
		response["status"] = "warning"
	}

	if stats.SystemGoroutines > 500 || stats.ManagedGoroutines >= stats.MaxGoroutines {
		response["status"] = "critical"
	}

	utils.SendResponse(c, nil, response)
}

// GetDetailedGoroutineStats 获取详细的协程统计信息
func GetDetailedGoroutineStats(c *gin.Context) {
	stats := utils.GlobalSafeGoroutineManager.GetGoroutineStats()

	// 获取更详细的信息
	detailedStats := map[string]interface{}{
		"basic_stats": stats,
		"memory_stats": map[string]interface{}{
			"alloc":      getMemoryUsage(),
			"goroutines": runtime.NumGoroutine(),
		},
		"sync_stats": map[string]interface{}{
			"unified_sync_goroutines": atomic.LoadInt32(&currentUnifiedSyncs),
			"max_unified_syncs":       maxUnifiedSyncs,
			"usage_percentage":        float64(atomic.LoadInt32(&currentUnifiedSyncs)) / float64(maxUnifiedSyncs) * 100,
		},
	}

	utils.SendResponse(c, nil, detailedStats)
}

// getMemoryUsage 获取内存使用情况
func getMemoryUsage() map[string]interface{} {
	var m runtime.MemStats
	runtime.GC() // 强制GC以获得更准确的内存统计
	runtime.ReadMemStats(&m)

	return map[string]interface{}{
		"alloc_mb":       float64(m.Alloc) / 1024 / 1024,
		"total_alloc_mb": float64(m.TotalAlloc) / 1024 / 1024,
		"sys_mb":         float64(m.Sys) / 1024 / 1024,
		"num_gc":         m.NumGC,
		"heap_objects":   m.HeapObjects,
	}
}

// MonitorGoroutines 协程监控端点，用于健康检查
func MonitorGoroutines(c *gin.Context) {
	stats := utils.GlobalSafeGoroutineManager.GetGoroutineStats()

	status := "healthy"
	issues := make([]string, 0)

	// 检查各种异常情况
	if stats.SystemGoroutines > 500 {
		status = "critical"
		issues = append(issues, "系统协程数过多")
	} else if stats.SystemGoroutines > 300 {
		status = "warning"
		issues = append(issues, "系统协程数较高")
	}

	if stats.ManagedGoroutines >= stats.MaxGoroutines {
		status = "critical"
		issues = append(issues, "管理协程数达到上限")
	} else if stats.ManagedGoroutines > stats.MaxGoroutines*80/100 {
		if status != "critical" {
			status = "warning"
		}
		issues = append(issues, "管理协程数接近上限")
	}

	if len(stats.LongRunning) > 10 {
		status = "critical"
		issues = append(issues, "长时间运行协程过多")
	} else if len(stats.LongRunning) > 5 {
		if status != "critical" {
			status = "warning"
		}
		issues = append(issues, "长时间运行协程较多")
	}

	// 设置HTTP状态码
	var httpStatus int
	switch status {
	case "healthy":
		httpStatus = 200
	case "warning":
		httpStatus = 200 // 警告仍然返回200
	case "critical":
		httpStatus = 503 // 严重问题返回503
	default:
		httpStatus = 200
	}

	response := map[string]interface{}{
		"status":    status,
		"issues":    issues,
		"stats":     stats,
		"timestamp": time.Now(),
	}

	c.JSON(httpStatus, response)
}

// ForceCleanupGoroutines 强制清理协程
func ForceCleanupGoroutines(c *gin.Context) {
	// 获取超时参数，默认30分钟
	timeoutMinutes := 30
	if timeoutStr := c.Query("timeout_minutes"); timeoutStr != "" {
		if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
			timeoutMinutes = t
		}
	}

	timeout := time.Duration(timeoutMinutes) * time.Minute
	cleanedCount := utils.GlobalSafeGoroutineManager.CleanupTimeoutGoroutines(timeout)

	message := fmt.Sprintf("强制清理了 %d 个超时协程（超过 %d 分钟）", cleanedCount, timeoutMinutes)
	log.Printf("[协程管理] %s", message)

	utils.SendResponse(c, nil, map[string]interface{}{
		"message":         message,
		"cleaned_count":   cleanedCount,
		"timeout_minutes": timeoutMinutes,
	})
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
