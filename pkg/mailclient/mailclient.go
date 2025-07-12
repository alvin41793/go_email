package mailclient

import (
	"crypto/tls"
	"fmt"
	"log"
	"strings"
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
		ticker := time.NewTicker(2 * time.Minute) // 每2分钟清理一次，缩短清理间隔
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
		// 如果连接超过10分钟未使用，则关闭（进一步缩短超时时间，防止网络超时）
		if now.Sub(conn.LastUsed) > 10*time.Minute {
			log.Printf("[连接池] 清理过期连接: %s (闲置时间: %v)", email, now.Sub(conn.LastUsed))
			conn.mutex.Lock()
			if conn.Client != nil {
				safeCloseConnection(conn.Client)
				conn.Client = nil
			}
			conn.mutex.Unlock()
			delete(p.connections, email)
		}
	}
}

// 获取或创建连接
func (p *ConnectionPool) GetConnection(config *EmailConfigInfo) (*client.Client, error) {
	return p.getConnectionWithRetry(config, 3)
}

// 带重试的获取连接
func (p *ConnectionPool) getConnectionWithRetry(config *EmailConfigInfo, maxRetries int) (*client.Client, error) {
	email := config.EmailAddress

	for attempt := 1; attempt <= maxRetries; attempt++ {
		conn, err := p.tryGetConnection(config)
		if err == nil && conn != nil {
			log.Printf("[连接池] 连接获取成功 (尝试 %d/%d): %s", attempt, maxRetries, email)
			return conn, nil
		}

		log.Printf("[连接池] 获取连接失败 (尝试 %d/%d): %s, 错误: %v", attempt, maxRetries, email, err)

		// 如果不是最后一次尝试，清理可能存在的坏连接并等待
		if attempt < maxRetries {
			p.CloseConnection(email)
			// 使用指数退避策略
			delay := time.Second * time.Duration(attempt*2)
			log.Printf("[连接池] 等待 %v 后重试连接: %s", delay, email)
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("获取连接失败，已重试 %d 次", maxRetries)
}

// 尝试获取连接（单次）
func (p *ConnectionPool) tryGetConnection(config *EmailConfigInfo) (*client.Client, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	email := config.EmailAddress

	// 检查是否已有连接
	if pooledConn, exists := p.connections[email]; exists {
		pooledConn.mutex.Lock()
		defer pooledConn.mutex.Unlock()

		// 检查连接是否仍然有效
		if pooledConn.Client != nil {
			// 多重健康检查
			if p.isConnectionHealthy(pooledConn.Client, email) {
				// 连接有效，更新最后使用时间
				pooledConn.LastUsed = time.Now()
				log.Printf("[连接池] 复用现有连接: %s, 状态: %v", email, pooledConn.Client.State())
				return pooledConn.Client, nil
			} else {
				log.Printf("[连接池] 连接已失效，清理并重新创建: %s", email)
				// 连接失效，安全地清理
				safeCloseConnection(pooledConn.Client)
				pooledConn.Client = nil
			}
		}
	}

	// 创建新连接
	log.Printf("[连接池] 创建新连接: %s", email)
	client, err := createNewConnection(config)
	if err != nil {
		// 清理失败的连接记录
		delete(p.connections, email)
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

// 检查是否是连接相关的错误
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"short write",
		"connection closed",
		"connection reset",
		"broken pipe",
		"network is unreachable",
		"timeout",
		"eof",
		"no such host",
		"connection refused",
		"connection timed out",
		"use of closed network connection",
		"read tcp",
		"write tcp",
		"i/o timeout",
		"connection lost",
		"network error",
		"socket closed",
		"bad sequence",                     // IMAP协议错误，通常由连接状态不一致引起
		"connection error",                 // 明确标识的连接错误
		"server error",                     // 服务器临时错误
		"please try again later",           // 服务器建议重试的错误
		"temporary failure",                // 临时故障
		"service unavailable",              // 服务不可用
		"internal server error",            // 内部服务器错误
		"server busy",                      // 服务器繁忙
		"resource temporarily unavailable", // 资源临时不可用
		"operation timed out",              // 操作超时
		"read: operation timed out",        // 读取操作超时
		"write: operation timed out",       // 写入操作超时
		"error reading response",           // 读取响应错误
		"error writing request",            // 写入请求错误
		"dial timeout",                     // 连接超时
		"context deadline exceeded",        // 上下文超时
		"context canceled",                 // 上下文取消
		"network connection timed out",     // 网络连接超时
		"tcp timeout",                      // TCP超时
		"ssl handshake timeout",            // SSL握手超时
		"tls handshake timeout",            // TLS握手超时
	}

	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}

	return false
}

// 安全地关闭连接
func safeCloseConnection(c *client.Client) {
	if c == nil {
		return
	}

	// 使用recover来处理可能的panic
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[连接池] 关闭连接时发生异常: %v", r)
		}
	}()

	// 检查连接状态，只有在非关闭状态时才尝试logout
	state := c.State()
	if state != 0 && state != 4 { // Closed=0, Logout=4
		if err := c.Logout(); err != nil {
			log.Printf("[连接池] 连接logout失败: %v", err)
		}
	}
}

// 连接健康检查
func (p *ConnectionPool) isConnectionHealthy(c *client.Client, email string) bool {
	// 检查1: 连接状态
	state := c.State()
	if state == 0 || state == 4 { // Closed=0, Logout=4 in go-imap v1
		log.Printf("[连接池] 连接已关闭: %s, 状态: %v", email, state)
		return false
	}

	// 检查2: 验证是否在正确的状态
	if state != 2 && state != 6 { // Auth=2, Selected=6 in go-imap v1
		log.Printf("[连接池] 连接状态异常: %s, 状态: %v", email, state)
		return false
	}

	// 检查3: NOOP命令（更安全的检查）- 设置更短的超时时间
	noopStart := time.Now()
	err := c.Noop()
	noopDuration := time.Since(noopStart)

	if err != nil {
		log.Printf("[连接池] NOOP命令失败: %s, 耗时: %v, 错误: %v", email, noopDuration, err)
		// 检查是否是连接相关的错误或IMAP命令错误
		if isConnectionError(err) || strings.Contains(strings.ToLower(err.Error()), "command is not a valid imap command") {
			log.Printf("[连接池] NOOP失败，检测到连接或命令错误: %s", email)
			return false
		}
		// 非连接错误，可能是临时问题，再次验证状态
		currentState := c.State()
		if currentState == 0 || currentState == 4 {
			log.Printf("[连接池] NOOP失败后连接状态异常: %s, 状态: %v", email, currentState)
			return false
		}
		// 如果状态正常但NOOP失败，可能是临时问题，记录警告但继续使用
		log.Printf("[连接池] NOOP失败但连接状态正常: %s, 状态: %v, 将继续使用", email, currentState)
	}

	// 检查4: 如果NOOP耗时过长，也认为连接不健康
	if noopDuration > 10*time.Second {
		log.Printf("[连接池] NOOP响应过慢: %s, 耗时: %v, 认为连接不健康", email, noopDuration)
		return false
	}

	log.Printf("[连接池] 连接健康检查通过: %s, 状态: %v, NOOP耗时: %v", email, state, noopDuration)
	return true
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

		// 创建TLS配置
		tlsConfig := &tls.Config{
			ServerName:         config.IMAPServer,
			InsecureSkipVerify: false,
		}

		// 如果使用SSL，则使用TLS连接
		if config.UseSSL {
			c, err = client.DialTLS(fmt.Sprintf("%s:%d", config.IMAPServer, config.IMAPPort), tlsConfig)
		} else {
			c, err = client.Dial(fmt.Sprintf("%s:%d", config.IMAPServer, config.IMAPPort))
			if err == nil {
				if err = c.StartTLS(tlsConfig); err != nil {
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
			safeCloseConnection(pooledConn.Client)
			pooledConn.Client = nil
		}
		pooledConn.mutex.Unlock()
		delete(p.connections, email)
	}
}

// 重置连接状态 - 用于处理IMAP命令错误
func (p *ConnectionPool) ResetConnection(email string) {
	log.Printf("[连接池] 重置连接状态: %s", email)
	p.CloseConnection(email)

	// 短暂等待，确保连接完全关闭
	time.Sleep(100 * time.Millisecond)
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
		EmailAddress: account.Account,
		password: REDACTED
		IMAPPort:     993,
		UseSSL:       true,
	}, nil
}
