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
	listEmailsMutex      sync.Mutex
	listEmailsByUidMutex sync.Mutex
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
func newMailClient() *mailclient.MailClient {
	return mailclient.NewMailClient(
		mailConfig.IMAPServer,
		mailConfig.SMTPServer,
		mailConfig.EmailAddress,
		mailConfig.Password,
		mailConfig.IMAPPort,
		mailConfig.SMTPPort,
		mailConfig.UseSSL,
	)
}

// 获取邮件列表
func ListEmails(c *gin.Context) {
	// 使用互斥锁确保同一时间只有一个请求在处理邮件列表
	// 注意：这种方式会阻塞所有请求，可能影响性能
	// 如果需要更好的性能，可以考虑使用数据库事务和条件更新
	listEmailsMutex.Lock()
	defer listEmailsMutex.Unlock()

	// 为每个请求创建独立的邮件客户端实例
	mailClient := newMailClient()

	var req ListEmailsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
		return
	}

	folder := req.Folder
	limit := req.Limit

	// 使用数据库事务获取最新邮件ID并处理邮件
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	lastEmail, err := model.GetLatestEmailWithTx(tx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果没有记录，设置最大ID为0
			fmt.Println("数据库中没有邮件记录，可能为第一次同步")
		} else {
			// 其他错误
			tx.Rollback()
			utils.SendResponse(c, fmt.Errorf("获取最大email_id失败: %v", err), nil)
			return
		}
	}

	var emailsResult []mailclient.EmailInfo
	if lastEmail.EmailID > 0 {
		fmt.Printf("当前数据库最大email_id: %d\n", lastEmail.EmailID)
		startUID := lastEmail.EmailID
		endUID := startUID + limit
		// 使用UID范围获取邮件
		emailsResult, err = mailClient.ListEmails(folder, limit, uint32(startUID), uint32(endUID))
	} else {
		// 获取最新邮件（原有功能）
		emailsResult, err = mailClient.ListEmails(folder, limit)
	}

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
		if email.HasAttachments == true {
			emailInfo.HasAttachment = 1
		}
		emailInfo.CreatedAt = utils.JsonTime{Time: time.Now()}

		emailList = append(emailList, &emailInfo)
	}

	err = model.BatchCreateEmailsWithTx(emailList, tx)
	if err != nil {
		tx.Rollback()
		utils.SendResponse(c, err, nil)
		return
	}

	if err := tx.Commit().Error; err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	utils.SendResponse(c, nil, emailsResult)
}

func ListEmailsByUid(c *gin.Context) {
	// 使用互斥锁确保同一时间只有一个请求在处理邮件列表
	listEmailsByUidMutex.Lock()
	defer listEmailsByUidMutex.Unlock()

	// 为每个请求创建独立的邮件客户端实例
	mailClient := newMailClient()

	var req ListEmailsByUidRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
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
	emailsResult, err := mailClient.ListEmails("INBOX", count, uint32(startUID), uint32(endUID))

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
		if email.HasAttachments == true {
			emailInfo.HasAttachment = 1
		}
		emailInfo.CreatedAt = utils.JsonTime{Time: time.Now()}

		emailList = append(emailList, &emailInfo)
	}

	err = model.BatchCreateEmailsWithTx(emailList, tx)
	if err != nil {
		tx.Rollback()
		utils.SendResponse(c, err, nil)
		return
	}

	if err := tx.Commit().Error; err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	utils.SendResponse(c, nil, emailsResult)
}

// GetEmailContent 获取邮件内容
func GetEmailContent(c *gin.Context) {
	// 为每个请求创建独立的邮件客户端实例
	mailClient := newMailClient()

	var req GetEmailContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
		return
	}

	folder := req.Folder
	limit := req.Limit

	// 获取状态为-1的邮件ID，并将其状态更新为0（处理中）
	emailIDs, err := model.GetEmailByStatus(-1, limit)
	if err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	// 检查是否有邮件需要处理
	if len(emailIDs) == 0 {
		log.Printf("[邮件处理] 没有需要处理的新邮件")
		fmt.Println("没有需要处理的新邮件")
		utils.SendResponse(c, nil, "没有需要处理的新邮件")
		return
	}

	log.Printf("[邮件处理] 开始处理 %d 封邮件, 文件夹: %s", len(emailIDs), folder)
	fmt.Printf("\n========== 开始处理 %d 封邮件，文件夹: %s ==========\n", len(emailIDs), folder)

	// 存储所有邮件内容和附件，以便后续批量存储
	type EmailData struct {
		EmailID      int
		EmailContent *model.PrimeEmailContent
		Attachments  []*model.PrimeEmailContentAttachment
	}

	allEmailData := make([]EmailData, 0, len(emailIDs))

	// 第一步：获取所有邮件内容
	fmt.Printf("\n【第1阶段】获取所有邮件内容...\n")
	for _, emailID := range emailIDs {
		log.Printf("[邮件处理] 正在获取邮件内容，ID: %d", emailID)
		fmt.Printf("  • 获取邮件 ID: %d 内容... ", emailID)

		email, err := mailClient.GetEmailContent(uint32(emailID), folder)
		if err != nil {
			log.Printf("[邮件处理] 获取邮件内容失败，邮件ID: %d, 错误: %v", emailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			// 如果获取失败，将邮件状态置为-2.
			resetErr := model.ResetEmailStatus(emailID, -2)
			if resetErr != nil {
				log.Printf("[邮件处理] 设置邮件状态失败，邮件ID: %d, 错误: %v", emailID, resetErr)
			}
			utils.SendResponse(c, err, nil)
			return
		}

		log.Printf("[邮件处理] 成功获取邮件内容，邮件ID: %d, 主题: %s, 发件人: %s", emailID, email.Subject, email.From)
		fmt.Printf("✅ 成功，主题: %s\n", email.Subject)

		// 创建邮件内容记录
		emailContent := &model.PrimeEmailContent{
			EmailID:     emailID,
			Subject:     utils.SanitizeUTF8(email.Subject),
			FromEmail:   utils.SanitizeUTF8(email.From),
			ToEmail:     utils.SanitizeUTF8(email.To),
			Date:        utils.SanitizeUTF8(email.Date),
			Content:     utils.SanitizeUTF8(email.Body),
			HTMLContent: utils.SanitizeUTF8(email.BodyHTML),
			Type:        0,
			CreatedAt:   utils.JsonTime{Time: time.Now()},
			UpdatedAt:   utils.JsonTime{Time: time.Now()},
		}

		// 创建附件记录列表
		attachmentRecords := make([]*model.PrimeEmailContentAttachment, 0)
		if len(email.Attachments) > 0 {
			log.Printf("[邮件处理] 邮件含有 %d 个附件，邮件ID: %d", len(email.Attachments), emailID)
			fmt.Printf("    📎 发现 %d 个附件\n", len(email.Attachments))

			// 处理附件
			for i, attachment := range email.Attachments {
				log.Printf("[附件处理] 开始处理附件 %d/%d，邮件ID: %d, 文件名: %s",
					i+1, len(email.Attachments), emailID, attachment.Filename)
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

					log.Printf("[附件处理] 开始上传附件到OSS，邮件ID: %d, 文件名: %s", emailID, attachment.Filename)
					fmt.Printf("        正在上传到OSS... ")
					var err error
					// 添加重试机制，最多尝试2次
					maxRetries := 2
					for attempt := 1; attempt <= maxRetries; attempt++ {
						log.Printf("[附件处理] 尝试上传附件到OSS (尝试 %d/%d)，邮件ID: %d, 文件名: %s",
							attempt, maxRetries, emailID, attachment.Filename)
						if attempt > 1 {
							fmt.Printf("        重试上传到OSS (尝试 %d/%d)... ", attempt, maxRetries)
						} else {
							fmt.Printf("        正在上传到OSS... ")
						}

						ossURL, err = oss.UploadBase64ToOSS(attachment.Filename, attachment.Base64Data, fileType)
						if err == nil {
							// 上传成功，跳出循环
							log.Printf("[附件处理] 成功上传附件到OSS，邮件ID: %d, 文件名: %s, URL: %s", emailID, attachment.Filename, ossURL)
							fmt.Printf("✅ 成功\n")
							break
						}

						// 上传失败
						if attempt < maxRetries {
							log.Printf("[附件处理] 上传附件到OSS失败，准备重试，邮件ID: %d, 文件名: %s, 错误: %v",
								emailID, attachment.Filename, err)
							fmt.Printf("❌ 失败: %v，准备重试\n", err)
							// 可以在这里添加短暂的延迟
							time.Sleep(time.Second * 2)
						} else {
							// 最后一次尝试也失败了
							log.Printf("[附件处理] 上传附件到OSS失败，已达到最大重试次数，邮件ID: %d, 文件名: %s, 错误: %v",
								emailID, attachment.Filename, err)
							fmt.Printf("❌ 最终失败: %v\n", err)
						}
					}

					// 检查是否所有尝试都失败了
					if err != nil {
						fmt.Printf("[附件处理] 经过 %d 次尝试，上传附件到OSS仍然失败，邮件ID: %d, 文件名: %s\n",
							maxRetries, emailID, attachment.Filename)
					}
				} else {
					log.Printf("[附件处理] 附件没有Base64数据，邮件ID: %d, 文件名: %s", emailID, attachment.Filename)
					fmt.Printf("        附件没有Base64数据，跳过上传\n")
				}

				// 创建附件记录
				attachmentRecord := &model.PrimeEmailContentAttachment{
					EmailID:   emailID,
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
			log.Printf("[邮件处理] 邮件没有附件，邮件ID: %d", emailID)
			fmt.Printf("    📄 邮件没有附件\n")
		}

		// 添加到待处理列表
		allEmailData = append(allEmailData, EmailData{
			EmailID:      emailID,
			EmailContent: emailContent,
			Attachments:  attachmentRecords,
		})
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
			utils.SendResponse(c, err, nil)
			return
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
					utils.SendResponse(c, err, nil)
					return
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
			utils.SendResponse(c, err, nil)
			return
		}

		fmt.Printf("✅ 成功\n")
	}

	// 提交事务
	fmt.Printf("\n◉ 提交事务... ")
	if err := tx.Commit().Error; err != nil {
		log.Printf("[邮件处理] 提交事务失败，错误: %v", err)
		fmt.Printf("❌ 失败: %v\n", err)
		tx.Rollback()
		utils.SendResponse(c, err, nil)
		return
	}

	log.Printf("[邮件处理] 成功提交事务，完成处理 %d 封邮件", len(emailIDs))
	fmt.Printf("✅ 成功\n")
	fmt.Printf("========== 成功完成处理 %d 封邮件 ==========\n\n", len(allEmailData))

	utils.SendResponse(c, nil, fmt.Sprintf("成功处理 %d 封邮件", len(allEmailData)))
}

// 列出邮件附件
func ListAttachments(c *gin.Context) {
	// 为每个请求创建独立的邮件客户端实例
	mailClient := newMailClient()

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

// GetMaxEmailID 获取数据库中最大的email_id
func GetMaxEmailID(c *gin.Context) {
	lastEmail, err := model.GetLatestEmail()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果没有记录，返回0
			utils.SendResponse(c, nil, map[string]int{"max_email_id": 0})
			return
		}
		// 其他错误
		utils.SendResponse(c, fmt.Errorf("获取最大email_id失败: %v", err), nil)
		return
	}

	// 返回最大的email_id
	utils.SendResponse(c, nil, map[string]int{"max_email_id": lastEmail.EmailID})
}

// ListEmailsRequest 获取邮件列表请求结构
type ListEmailsRequest struct {
	Folder string `json:"folder" binding:"required"`
	Limit  int    `json:"limit" binding:"required"`
}

// ListEmailsByUidRequest 根据UID获取邮件列表请求结构
type ListEmailsByUidRequest struct {
	StartUID uint64 `json:"start_uid" binding:"required"`
	EndUID   uint64 `json:"end_uid" binding:"required"`
}

// GetEmailContentRequest 获取邮件内容请求结构
type GetEmailContentRequest struct {
	Folder string `json:"folder" binding:"required"`
	Limit  int    `json:"limit" binding:"required"`
}

// 发送邮件请求结构
type SendEmailRequest struct {
	To          string `json:"to" binding:"required"`
	Subject     string `json:"subject" binding:"required"`
	Body        string `json:"body" binding:"required"`
	ContentType string `json:"content_type"` // "text" 或 "html"
}

// 发送邮件
func SendEmail(c *gin.Context) {
	// 为每个请求创建独立的邮件客户端实例
	mailClient := newMailClient()

	var req SendEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, "无效的参数")
		return
	}

	contentType := req.ContentType
	if contentType != "html" {
		contentType = "text"
	}

	err := mailClient.SendEmail(req.To, req.Subject, req.Body, contentType)
	if err != nil {
		utils.SendResponse(c, err, nil)

		return
	}
	utils.SendResponse(c, err, "邮件发送成功")
}
