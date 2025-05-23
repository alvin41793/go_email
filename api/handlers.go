package api

import (
	"fmt"
	"go_email/db"
	"go_email/model"
	"go_email/pkg/mailclient"
	"go_email/pkg/utils"
	"go_email/pkg/utils/oss"
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
		emailInfo.FromEmail = email.From
		emailInfo.Subject = email.Subject
		emailInfo.Date = email.Date
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
	folder := c.DefaultQuery("folder", "INBOX")

	// 开始数据库事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, emailID := range emailIDs {
		email, err := mailClient.GetEmailContent(uint32(emailID), folder)
		if err != nil {
			tx.Rollback()
			utils.SendResponse(c, err, nil)
			return
		}

		// 创建邮件内容记录
		emailContent := &model.PrimeEmailContent{
			EmailID:     emailID,
			Subject:     email.Subject,
			FromEmail:   email.From,
			ToEmail:     email.To,
			Date:        email.Date,
			Content:     email.Body,
			HTMLContent: email.BodyHTML,
			Type:        0, // 需要根据实际情况设置类型
		}

		// 在事务中保存邮件内容
		if err := emailContent.CreateWithTransaction(tx); err != nil {
			tx.Rollback()
			utils.SendResponse(c, err, nil)
			return
		}

		// 上传附件到OSS并保存附件记录（如果有）
		if len(email.Attachments) > 0 {
			var attachments []*model.PrimeEmailContentAttachment

			for i, attachment := range email.Attachments {
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
					ossURL, err := oss.UploadBase64ToOSS(attachment.Filename, attachment.Base64Data, fileType)
					if err != nil {
						fmt.Printf("上传附件到OSS失败: %v\n", err)
						// 继续处理其他附件，不中断流程
					} else {
						// 保存OSS URL
						email.Attachments[i].OssURL = ossURL
						fmt.Printf("附件 %s 上传到OSS成功，URL: %s\n", attachment.Filename, ossURL)
					}
				}

				// 创建附件记录
				attachmentRecord := &model.PrimeEmailContentAttachment{
					EmailID:  emailID,
					FileName: attachment.Filename,
					SizeKb:   attachment.SizeKB,
					MimeType: attachment.MimeType,
					OssUrl:   attachment.OssURL,
				}

				attachments = append(attachments, attachmentRecord)
			}

			// 批量创建附件记录
			if len(attachments) > 0 {
				if err := model.BatchCreateAttachmentsWithTransaction(tx, attachments); err != nil {
					tx.Rollback()
					utils.SendResponse(c, err, nil)
					return
				}
			}
		}

		// 更新邮件状态为已处理
		if err := tx.Model(&model.PrimeEmail{}).Where("email_id = ?", emailID).Update("status", 1).Error; err != nil {
			tx.Rollback()
			utils.SendResponse(c, err, nil)
			return
		}
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		utils.SendResponse(c, err, nil)
		return
	}

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
