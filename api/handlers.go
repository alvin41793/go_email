package api

import (
	"fmt"
	"net/http"
	"strconv"

	"go_email/pkg/mailclient"

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
	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = 10
	}

	emails, err := mailClient.ListEmails(folder, limit)
	if err != nil {
		HandleError(c, err)
		return
	}

	ResponseOK(c, emails)
}

// 获取邮件内容
func GetEmailContent(c *gin.Context) {
	uidStr := c.Param("uid")
	folder := c.DefaultQuery("folder", "INBOX")

	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		ResponseError(c, http.StatusBadRequest, "无效的UID")
		return
	}

	email, err := mailClient.GetEmailContent(uint32(uid), folder)
	if err != nil {
		HandleError(c, err)
		return
	}

	ResponseOK(c, email)
}

// 列出邮件附件
func ListAttachments(c *gin.Context) {
	uidStr := c.Param("uid")
	folder := c.DefaultQuery("folder", "INBOX")

	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		ResponseError(c, http.StatusBadRequest, "无效的UID")
		return
	}

	email, err := mailClient.GetEmailContent(uint32(uid), folder)
	if err != nil {
		HandleError(c, err)
		return
	}

	ResponseOK(c, email.Attachments)
}

// 下载附件
func DownloadAttachment(c *gin.Context) {
	uidStr := c.Param("uid")
	filename := c.Query("filename")
	folder := c.DefaultQuery("folder", "INBOX")

	if filename == "" {
		ResponseError(c, http.StatusBadRequest, "未提供文件名")
		return
	}

	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		ResponseError(c, http.StatusBadRequest, "无效的UID")
		return
	}

	// 获取附件内容
	data, contentType, err := mailClient.GetAttachment(uint32(uid), filename, folder)
	if err != nil {
		HandleError(c, err)
		return
	}

	// 下载附件是特殊情况，直接返回文件内容而不是JSON
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Data(http.StatusOK, contentType, data)
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
		ResponseError(c, http.StatusBadRequest, err.Error())
		return
	}

	contentType := req.ContentType
	if contentType != "html" {
		contentType = "text"
	}

	err := mailClient.SendEmail(req.To, req.Subject, req.Body, contentType)
	if err != nil {
		HandleError(c, err)
		return
	}

	ResponseOKWithMsg(c, "邮件发送成功", nil)
}
