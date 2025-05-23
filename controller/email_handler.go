package controller

import (
	"fmt"
	"go_email/db"
	"go_email/model"
	"go_email/pkg/mailclient"
	"go_email/pkg/oss"
	"go_email/pkg/utils"
	"strings"

	"github.com/gin-gonic/gin"
)

// SaveEmailContent 保存邮件内容到数据库
func SaveEmailContent(c *gin.Context, emailIDs []int, mailClient *mailclient.MailClient, folder string) error {
	// 开始数据库事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, emailID := range emailIDs {
		// 获取邮件详情
		email, err := mailClient.GetEmailContent(uint32(emailID), folder)
		if err != nil {
			tx.Rollback()
			return err
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
			return err
		}

		// 处理附件（如果有）
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
					return err
				}
			}
		}
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return err
	}

	return nil
}

// GetAndSaveEmailContent 处理获取和保存邮件内容的请求
func GetAndSaveEmailContent(c *gin.Context) {
	// 获取请求参数
	var req struct {
		EmailIDs []int  `json:"email_ids" binding:"required"`
		Folder   string `json:"folder"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	// 获取邮件客户端配置
	emailConfig, err := mailclient.GetEmailConfig()
	if err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	// 创建邮件客户端
	mailClient := mailclient.NewMailClient(
		emailConfig.IMAPServer,
		emailConfig.SMTPServer,
		emailConfig.EmailAddress,
		emailConfig.Password,
		emailConfig.IMAPPort,
		emailConfig.SMTPPort,
		emailConfig.UseSSL,
	)

	// 保存邮件内容
	if err := SaveEmailContent(c, req.EmailIDs, mailClient, req.Folder); err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	utils.SendResponse(c, nil, gin.H{
		"message": "邮件内容保存成功",
	})
}
