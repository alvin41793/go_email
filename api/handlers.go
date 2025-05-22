package api

import (
	"fmt"
	"go_email/api/oss"
	"go_email/pkg/mailclient"
	"go_email/pkg/utils"
	"strconv"
	"strings"

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
	folder := c.DefaultQuery("folder", "INBOX")
	limitStr := c.DefaultQuery("limit", "200")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = 10
	}

	emails, err := mailClient.ListEmails(folder, limit)
	if err != nil {
		utils.SendResponse(c, err, nil)
		return
	}

	utils.SendResponse(c, err, emails)
}

// 获取邮件内容
func GetEmailContent(c *gin.Context) {
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
	// 上传附件到OSS（如果有）
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

			// 上传完成后，清除base64数据，减少返回数据量
			email.Attachments[i].Base64Data = ""
		}
	}

	utils.SendResponse(c, err, email)

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
