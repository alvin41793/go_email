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
	"time"

	"github.com/gin-gonic/gin"
)

// 邮件客户端实例
var mailClient *mailclient.MailClient

// 初始化邮件客户端
func InitMailClient(imapServer, smtpServer, emailAddress, password string, imapPort, smtpPort int, useSSL bool) {
	mailClient = mailclient.NewMailClient(
		imapServer,
		smtpServer,
		emailAddress,
		password,
		imapPort,
		smtpPort,
		useSSL,
	)
}

// 获取邮件列表
func ListEmails(c *gin.Context) {
	//fmt.Println("请求邮箱列表")
	folder := c.DefaultQuery("folder", "INBOX")
	limitStr := c.DefaultQuery("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = 10
	}

	emails, err := mailClient.ListEmails(folder, limit)
	if err != nil {
		utils.SendResponse(c, err, nil)
		return
	}
	var emailList []*model.PrimeEmail
	for _, email := range emails {
		var emailInfo model.PrimeEmail
		emailInfo.EmailID, _ = strconv.Atoi(email.EmailID)
		emailInfo.FromEmail = utils.SanitizeUTF8(email.From)
		emailInfo.Subject = utils.SanitizeUTF8(email.Subject)
		emailInfo.Date = utils.SanitizeUTF8(email.Date)
		emailInfo.HasAttachment = 0
		emailInfo.Status = 0
		if email.HasAttachments == true {
			emailInfo.HasAttachment = 1
		}
		emailInfo.CreatedAt = utils.JsonTime{Time: time.Now()}

		emailList = append(emailList, &emailInfo)

	}
	err = model.BatchCreateEmails(emailList)
	if err != nil {
		utils.SendResponse(c, err, nil)
		return
	}
	utils.SendResponse(c, err, "存入邮件列表成功")
}

// 获取邮件内容
func GetEmailContent(c *gin.Context) {
	emailIDs, err := model.GetEmailByStatus(0, 10)
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

	folder := c.DefaultQuery("folder", "INBOX")
	log.Printf("[邮件处理] 开始处理 %d 封邮件, 文件夹: %s", len(emailIDs), folder)
	fmt.Printf("\n========== 开始处理 %d 封邮件，文件夹: %s ==========\n", len(emailIDs), folder)

	// 开始数据库事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("[邮件处理] 发生异常，事务回滚: %v", r)
			fmt.Printf("❌ 发生异常，事务回滚: %v\n", r)
		}
	}()

	for _, emailID := range emailIDs {
		log.Printf("[邮件处理] 正在处理邮件 ID: %d", emailID)
		fmt.Printf("\n----- 处理邮件 ID: %d -----\n", emailID)

		email, err := mailClient.GetEmailContent(uint32(emailID), folder)
		if err != nil {
			log.Printf("[邮件处理] 获取邮件内容失败，邮件ID: %d, 错误: %v", emailID, err)
			fmt.Printf("❌ 获取邮件内容失败: %v\n", err)
			tx.Rollback()
			utils.SendResponse(c, err, nil)
			return
		}
		log.Printf("[邮件处理] 成功获取邮件内容，邮件ID: %d, 主题: %s, 发件人: %s", emailID, email.Subject, email.From)
		fmt.Printf("✅ 获取邮件内容成功:\n  • 主题: %s\n  • 发件人: %s\n  • 收件人: %s\n",
			email.Subject, email.From, email.To)

		// 创建邮件内容记录
		emailContent := &model.PrimeEmailContent{
			EmailID:     emailID,
			Subject:     utils.SanitizeUTF8(email.Subject),
			FromEmail:   utils.SanitizeUTF8(email.From),
			ToEmail:     utils.SanitizeUTF8(email.To),
			Date:        utils.SanitizeUTF8(email.Date),
			Content:     utils.SanitizeUTF8(email.Body),
			HTMLContent: utils.SanitizeUTF8(email.BodyHTML),
			Type:        0, // 需要根据实际情况设置类型
		}

		// 确保CreatedAt和UpdatedAt有有效值，避免反射错误
		emailContent.CreatedAt = utils.JsonTime{Time: time.Now()}
		emailContent.UpdatedAt = utils.JsonTime{Time: time.Now()}

		// 在事务中保存邮件内容
		if err := emailContent.CreateWithTransaction(tx); err != nil {
			log.Printf("[邮件处理] 保存邮件内容到数据库失败，邮件ID: %d, 错误: %v", emailID, err)
			fmt.Printf("❌ 保存邮件内容到数据库失败: %v\n", err)
			tx.Rollback()
			utils.SendResponse(c, err, nil)
			return
		}
		log.Printf("[邮件处理] 成功保存邮件内容到数据库，邮件ID: %d", emailID)
		fmt.Printf("✅ 保存邮件内容到数据库成功\n")

		// 上传附件到OSS并保存附件记录（如果有）
		if len(email.Attachments) > 0 {
			log.Printf("[邮件处理] 邮件含有 %d 个附件，邮件ID: %d", len(email.Attachments), emailID)
			fmt.Printf("📎 发现 %d 个附件\n", len(email.Attachments))
			var attachments []*model.PrimeEmailContentAttachment

			for i, attachment := range email.Attachments {
				log.Printf("[附件处理] 开始处理附件 %d/%d，邮件ID: %d, 文件名: %s, 大小: %.2f KB, 类型: %s",
					i+1, len(email.Attachments), emailID, attachment.Filename, attachment.SizeKB, attachment.MimeType)
				fmt.Printf("  • 附件 %d/%d: %s (%.2f KB, %s)\n",
					i+1, len(email.Attachments), attachment.Filename, attachment.SizeKB, attachment.MimeType)

				if attachment.Base64Data != "" {
					// 确定文件类型
					fileType := ""
					if attachment.MimeType != "" {
						parts := strings.Split(attachment.MimeType, "/")
						if len(parts) > 1 {
							fileType = parts[1]
						}
					}
					// 上传到OSS
					log.Printf("[附件处理] 开始上传附件到OSS，邮件ID: %d, 文件名: %s", emailID, attachment.Filename)
					fmt.Printf("    - 正在上传到OSS... ")
					ossURL, err := oss.UploadBase64ToOSS(attachment.Filename, attachment.Base64Data, fileType)
					if err != nil {
						log.Printf("[附件处理] 上传附件到OSS失败，邮件ID: %d, 文件名: %s, 错误: %v", emailID, attachment.Filename, err)
						fmt.Printf("❌ 失败: %v\n", err)
						// 继续处理其他附件，不中断流程
					} else {
						// 保存OSS URL
						email.Attachments[i].OssURL = ossURL
						log.Printf("[附件处理] 成功上传附件到OSS，邮件ID: %d, 文件名: %s, URL: %s", emailID, attachment.Filename, ossURL)
						fmt.Printf("✅ 成功\n")
					}
				} else {
					log.Printf("[附件处理] 附件没有Base64数据，邮件ID: %d, 文件名: %s", emailID, attachment.Filename)
					fmt.Printf("    - 附件没有Base64数据，跳过上传\n")
				}

				// 创建附件记录
				attachmentRecord := &model.PrimeEmailContentAttachment{
					EmailID:  emailID,
					FileName: utils.SanitizeUTF8(attachment.Filename),
					SizeKb:   attachment.SizeKB,
					MimeType: utils.SanitizeUTF8(attachment.MimeType),
					OssUrl:   utils.SanitizeUTF8(attachment.OssURL),
				}

				// 确保CreatedAt和UpdatedAt有有效值，避免反射错误
				attachmentRecord.CreatedAt = utils.JsonTime{Time: time.Now()}
				attachmentRecord.UpdatedAt = utils.JsonTime{Time: time.Now()}

				attachments = append(attachments, attachmentRecord)
			}

			// 批量创建附件记录
			if len(attachments) > 0 {
				log.Printf("[附件处理] 准备批量保存 %d 个附件记录到数据库，邮件ID: %d", len(attachments), emailID)
				fmt.Printf("  • 保存 %d 个附件记录到数据库... ", len(attachments))
				if err := model.BatchCreateAttachmentsWithTransaction(tx, attachments); err != nil {
					log.Printf("[附件处理] 保存附件记录到数据库失败，邮件ID: %d, 错误: %v", emailID, err)
					fmt.Printf("❌ 失败: %v\n", err)
					tx.Rollback()
					utils.SendResponse(c, err, nil)
					return
				}
				log.Printf("[附件处理] 成功保存 %d 个附件记录到数据库，邮件ID: %d", len(attachments), emailID)
				fmt.Printf("✅ 成功\n")
			}
		} else {
			log.Printf("[邮件处理] 邮件没有附件，邮件ID: %d", emailID)
			fmt.Printf("📎 邮件没有附件\n")
		}

		// 更新邮件状态为已处理
		log.Printf("[邮件处理] 更新邮件状态为已处理，邮件ID: %d", emailID)
		fmt.Printf("  • 更新邮件状态为已处理... ")
		if err := tx.Model(&model.PrimeEmail{}).Where("email_id = ?", emailID).Update("status", 1).Error; err != nil {
			log.Printf("[邮件处理] 更新邮件状态失败，邮件ID: %d, 错误: %v", emailID, err)
			fmt.Printf("❌ 失败: %v\n", err)
			tx.Rollback()
			utils.SendResponse(c, err, nil)
			return
		}
		log.Printf("[邮件处理] 成功更新邮件状态为已处理，邮件ID: %d", emailID)
		fmt.Printf("✅ 成功\n")
		fmt.Printf("----- 邮件 ID: %d 处理完成 -----\n", emailID)
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
	fmt.Printf("========== 成功完成处理 %d 封邮件 ==========\n\n", len(emailIDs))

	utils.SendResponse(c, nil, "邮件内容获取并保存成功")
}

// 列出邮件附件
func ListAttachments(c *gin.Context) {
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

// 发送邮件请求结构
type SendEmailRequest struct {
	To          string `json:"to" binding:"required"`
	Subject     string `json:"subject" binding:"required"`
	Body        string `json:"body" binding:"required"`
	ContentType string `json:"content_type"` // "text" 或 "html"
}

// 发送邮件
func SendEmail(c *gin.Context) {
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
