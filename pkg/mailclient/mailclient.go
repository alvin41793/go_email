package mailclient

import (
	"fmt"

	"go_email/model"

	"github.com/emersion/go-imap/client"
)

// EmailConfig 应用配置结构体
type EmailConfig struct {
	Email struct {
		IMAPServer   string `yaml:"imap_server"`
		SMTPServer   string `yaml:"smtp_server"`
		EmailAddress string `yaml:"email_address"`
		Password     string `yaml:"password"`
		IMAPPort     int    `yaml:"imap_port"`
		SMTPPort     int    `yaml:"smtp_port"`
		UseSSL       bool   `yaml:"use_ssl"`
	} `yaml:"email"`

	Server struct {
		Port int    `yaml:"port"`
		Host string `yaml:"host"`
	} `yaml:"server"`
}

// EmailConfigInfo 邮箱配置
type EmailConfigInfo struct {
	IMAPServer   string
	SMTPServer   string
	EmailAddress string
	Password     string
	IMAPPort     int
	SMTPPort     int
	UseSSL       bool
}

// MailClient 结构体，用于处理邮件收发
type MailClient struct {
	IMAPServer   string
	SMTPServer   string
	EmailAddress string
	Password     string
	IMAPPort     int
	SMTPPort     int
	UseSSL       bool
}

// EmailInfo 邮件信息结构体
type EmailInfo struct {
	EmailID        string `json:"email_id"`
	Subject        string `json:"subject"`
	From           string `json:"from"`
	Date           string `json:"date"`
	UID            uint32 `json:"uid"`
	HasAttachments bool   `json:"has_attachments"`
}

// AttachmentInfo 附件信息结构体
type AttachmentInfo struct {
	Filename   string  `json:"filename"`
	SizeKB     float64 `json:"size_kb"`
	MimeType   string  `json:"mime_type"`
	Base64Data string  `json:"base64_data,omitempty"` // base64编码的附件内容
	OssURL     string  `json:"oss_url,omitempty"`     // OSS存储链接
}

// Email 结构体，包含邮件完整内容
type Email struct {
	EmailID     string           `json:"email_id"`
	Subject     string           `json:"subject"`
	From        string           `json:"from"`
	To          string           `json:"to"`
	Date        string           `json:"date"`
	Body        string           `json:"body"`
	BodyHTML    string           `json:"body_html"`
	Attachments []AttachmentInfo `json:"attachments"`
}

// NewMailClient 创建一个新的邮件客户端
func NewMailClient(imapServer, smtpServer, emailAddress, password string, imapPort, smtpPort int, useSSL bool) *MailClient {
	return &MailClient{
		IMAPServer:   imapServer,
		SMTPServer:   smtpServer,
		EmailAddress: emailAddress,
		password: REDACTED
		IMAPPort:     imapPort,
		SMTPPort:     smtpPort,
		UseSSL:       useSSL,
	}
}

// ConnectIMAP 连接到IMAP服务器
func (m *MailClient) ConnectIMAP() (*client.Client, error) {
	var c *client.Client
	var err error

	// 如果使用SSL，则使用TLS连接
	if m.UseSSL {
		c, err = client.DialTLS(fmt.Sprintf("%s:%d", m.IMAPServer, m.IMAPPort), nil)
	} else {
		c, err = client.Dial(fmt.Sprintf("%s:%d", m.IMAPServer, m.IMAPPort))
		if err == nil {
			if err = c.StartTLS(nil); err != nil {
				c.Logout()
				return nil, fmt.Errorf("StartTLS失败: %w", err)
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("连接IMAP服务器失败: %w", err)
	}

	// 登录
	if err := c.Login(m.EmailAddress, m.Password); err != nil {
		c.Logout()
		return nil, fmt.Errorf("IMAP登录失败: %w", err)
	}

	return c, nil
}

// GetEmailConfig 从数据库获取邮箱配置
func GetEmailConfig() (*EmailConfigInfo, error) {
	account, err := model.GetActiveAccount()
	if err != nil {
		return nil, fmt.Errorf("查询邮箱账号失败: %w", err)
	}

	return &EmailConfigInfo{
		IMAPServer:   "imap.ipage.com",
		SMTPServer:   "smtp.ipage.com",
		EmailAddress: account.Account,
		password: REDACTED
		IMAPPort:     993,
		SMTPPort:     587,
		UseSSL:       true,
	}, nil
}
