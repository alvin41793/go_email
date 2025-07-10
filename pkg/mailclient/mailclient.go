package mailclient

import (
	"fmt"
	"log"
	"sync"
	"time"

	"go_email/model"

	"github.com/emersion/go-imap/client"
)

// 连接池结构
type ConnectionPool struct {
	connections map[string]*PooledConnection
	mutex       sync.RWMutex
}

// 池化连接结构
type PooledConnection struct {
	Client      *client.Client
	LastUsed    time.Time
	AccountInfo *EmailConfigInfo
	mutex       sync.Mutex
}

// 全局连接池
var globalPool = &ConnectionPool{
	connections: make(map[string]*PooledConnection),
}

// 定期清理过期连接
func init() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute) // 每5分钟清理一次
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				globalPool.cleanupExpiredConnections()
			}
		}
	}()
}

// 清理过期连接
func (p *ConnectionPool) cleanupExpiredConnections() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	now := time.Now()
	for email, conn := range p.connections {
		// 如果连接超过30分钟未使用，则关闭
		if now.Sub(conn.LastUsed) > 30*time.Minute {
			log.Printf("[连接池] 清理过期连接: %s", email)
			conn.mutex.Lock()
			if conn.Client != nil {
				conn.Client.Logout()
				conn.Client = nil
			}
			conn.mutex.Unlock()
			delete(p.connections, email)
		}
	}
}

// 获取或创建连接
func (p *ConnectionPool) GetConnection(config *EmailConfigInfo) (*client.Client, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	email := config.EmailAddress

	// 检查是否已有连接
	if pooledConn, exists := p.connections[email]; exists {
		pooledConn.mutex.Lock()
		defer pooledConn.mutex.Unlock()

		// 检查连接是否仍然有效
		if pooledConn.Client != nil {
			// 尝试发送NOOP命令检查连接状态
			if err := pooledConn.Client.Noop(); err == nil {
				// 连接有效，更新最后使用时间
				pooledConn.LastUsed = time.Now()
				log.Printf("[连接池] 复用现有连接: %s", email)
				return pooledConn.Client, nil
			} else {
				log.Printf("[连接池] 连接已失效，需要重新连接: %s, 错误: %v", email, err)
				// 连接失效，清理并重新创建
				pooledConn.Client.Logout()
				pooledConn.Client = nil
			}
		}
	}

	// 创建新连接
	log.Printf("[连接池] 创建新连接: %s", email)
	client, err := createNewConnection(config)
	if err != nil {
		return nil, err
	}

	// 保存到连接池
	p.connections[email] = &PooledConnection{
		Client:      client,
		LastUsed:    time.Now(),
		AccountInfo: config,
	}

	return client, nil
}

// 创建新的IMAP连接
func createNewConnection(config *EmailConfigInfo) (*client.Client, error) {
	maxRetries := 3
	baseDelay := time.Second * 2

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("[IMAP连接] 尝试连接 %s:%d (尝试 %d/%d)", config.IMAPServer, config.IMAPPort, attempt, maxRetries)

		// 检查密码是否为空
		if config.password: REDACTED "" {
			return nil, fmt.Errorf("邮箱密码为空，请确认已设置应用专用密码")
		}

		var c *client.Client
		var err error

		// 如果使用SSL，则使用TLS连接
		if config.UseSSL {
			c, err = client.DialTLS(fmt.Sprintf("%s:%d", config.IMAPServer, config.IMAPPort), nil)
		} else {
			c, err = client.Dial(fmt.Sprintf("%s:%d", config.IMAPServer, config.IMAPPort))
			if err == nil {
				if err = c.StartTLS(nil); err != nil {
					c.Logout()
					log.Printf("[IMAP连接] StartTLS失败 (尝试 %d/%d): %v", attempt, maxRetries, err)
					if attempt < maxRetries {
						time.Sleep(baseDelay * time.Duration(attempt))
						continue
					}
					return nil, fmt.Errorf("StartTLS失败: %w", err)
				}
			}
		}

		if err != nil {
			log.Printf("[IMAP连接] 连接IMAP服务器失败 (尝试 %d/%d): %v", attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(baseDelay * time.Duration(attempt))
				continue
			}
			return nil, fmt.Errorf("连接IMAP服务器失败: %w", err)
		}

		// 登录
		log.Printf("[IMAP连接] 尝试登录邮箱: %s", config.EmailAddress)
		if err := c.Login(config.EmailAddress, config.Password); err != nil {
			c.Logout()
			log.Printf("[IMAP连接] IMAP登录失败 (尝试 %d/%d): %v", attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(baseDelay * time.Duration(attempt))
				continue
			}
			return nil, fmt.Errorf("IMAP登录失败: %w", err)
		}

		log.Printf("[IMAP连接] 成功连接并登录邮箱: %s", config.EmailAddress)
		return c, nil
	}

	return nil, fmt.Errorf("连接IMAP服务器失败，已重试 %d 次", maxRetries)
}

// 释放连接（将连接返回到池中）
func (p *ConnectionPool) ReleaseConnection(email string) {
	// 连接池管理的连接不需要手动释放，会自动管理
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	if pooledConn, exists := p.connections[email]; exists {
		pooledConn.mutex.Lock()
		pooledConn.LastUsed = time.Now()
		pooledConn.mutex.Unlock()
	}
}

// 强制关闭连接
func (p *ConnectionPool) CloseConnection(email string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if pooledConn, exists := p.connections[email]; exists {
		pooledConn.mutex.Lock()
		if pooledConn.Client != nil {
			log.Printf("[连接池] 强制关闭连接: %s", email)
			pooledConn.Client.Logout()
			pooledConn.Client = nil
		}
		pooledConn.mutex.Unlock()
		delete(p.connections, email)
	}
}

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
	Config *EmailConfigInfo
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
func NewMailClient(config *EmailConfigInfo) *MailClient {
	return &MailClient{
		Config: config,
	}
}

// ConnectIMAP 连接到IMAP服务器，使用连接池
func (m *MailClient) ConnectIMAP() (*client.Client, error) {
	return globalPool.GetConnection(m.Config)
}

// GetEmailConfig 从数据库获取邮箱配置
func GetEmailConfig(account model.PrimeEmailAccount) (*EmailConfigInfo, error) {
	// 检查应用专用密码是否设置
	password: REDACTED account.AppPassword
	if password: REDACTED "" {
		password: REDACTED
		log.Printf("[邮箱配置] 警告: AppPassword为空，使用普通密码，邮箱: %s", account.Account)
	} else {
		log.Printf("[邮箱配置] 使用应用专用密码，邮箱: %s", account.Account)
	}

	if password: REDACTED "" {
		return nil, fmt.Errorf("邮箱密码为空，请设置Password或AppPassword字段")
	}

	return &EmailConfigInfo{
		IMAPServer:   "imap.mail.yahoo.com",
		SMTPServer:   "smtp.mail.yahoo.com",
		EmailAddress: account.Account,
		password: REDACTED
		IMAPPort:     993,
		SMTPPort:     587,
		UseSSL:       true,
	}, nil
}

// GetConnectionPoolStats 获取连接池统计信息
func GetConnectionPoolStats() map[string]interface{} {
	globalPool.mutex.RLock()
	defer globalPool.mutex.RUnlock()

	stats := make(map[string]interface{})
	stats["total_connections"] = len(globalPool.connections)

	connections := make([]map[string]interface{}, 0)
	for email, conn := range globalPool.connections {
		connInfo := map[string]interface{}{
			"email":     email,
			"last_used": conn.LastUsed,
			"active":    conn.Client != nil,
		}
		connections = append(connections, connInfo)
	}
	stats["connections"] = connections

	return stats
}
