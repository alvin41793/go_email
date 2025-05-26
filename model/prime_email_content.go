package model

import (
	"go_email/db"
	"go_email/pkg/utils"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/jinzhu/gorm"
)

// PrimeEmailContent 邮件内容表结构
type PrimeEmailContent struct {
	ID          uint           `gorm:"primary_key;column:id" json:"id"`
	EmailID     int            `gorm:"column:email_id" json:"email_id"`
	Subject     string         `gorm:"column:subject;size:255" json:"subject"`                // 主题
	FromEmail   string         `gorm:"column:from_email;size:255" json:"from_email"`          // 发送者
	ToEmail     string         `gorm:"column:to_email;size:255" json:"to_email"`              // 接收者
	Date        string         `gorm:"column:date;size:255" json:"date"`                      // 邮件日期
	Content     string         `gorm:"column:content;type:text" json:"content"`               // 正文
	HTMLContent string         `gorm:"column:html_content;type:longtext" json:"html_content"` // html正文
	Type        int            `gorm:"column:type" json:"type"`                               // 邮件类型
	Status      int            `gorm:"column:status" json:"status"`
	CreatedAt   utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条邮件内容记录
func (e *PrimeEmailContent) Create() error {
	return db.DB().Create(e).Error
}

// GetContentByEmailID 根据EmailID获取邮件内容
func GetContentByEmailID(emailID int) (*PrimeEmailContent, error) {
	var content PrimeEmailContent
	err := db.DB().Where("email_id = ?", emailID).First(&content).Error
	return &content, err
}

// 清理非法UTF-8字符
func sanitizeUTF8(input string) string {
	if utf8.ValidString(input) {
		return input
	}

	log.Printf("[字符集处理] 检测到非法UTF-8字符，进行清洗")

	// 将非法UTF-8字符替换为空格
	result := strings.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return ' '
		}
		return r
	}, input)

	// 如果仍然有非法字符，使用更激进的方式：只保留ASCII字符
	if !utf8.ValidString(result) {
		log.Printf("[字符集处理] 第一次清洗后仍有非法字符，只保留ASCII字符")
		result = strings.Map(func(r rune) rune {
			if r <= 127 {
				return r
			}
			return ' '
		}, input)
	}

	return result
}

// CreateWithTransaction 使用事务创建邮件内容
func (e *PrimeEmailContent) CreateWithTransaction(tx *gorm.DB) error {
	log.Printf("[邮件内容保存] 准备保存邮件内容: ID=%d, 主题=%s, 发件人=%s", e.EmailID, e.Subject, e.FromEmail)

	// 清理所有文本字段，确保它们是有效的UTF-8字符串
	e.Subject = utils.SanitizeUTF8(e.Subject)
	e.FromEmail = utils.SanitizeUTF8(e.FromEmail)
	e.ToEmail = utils.SanitizeUTF8(e.ToEmail)
	e.Date = utils.SanitizeUTF8(e.Date)
	e.Content = utils.SanitizeUTF8(e.Content)
	e.HTMLContent = utils.SanitizeUTF8(e.HTMLContent)
	e.Status = -1
	err := tx.Create(e).Error
	if err != nil {
		log.Printf("[邮件内容保存] 保存邮件内容失败: ID=%d, 错误=%v", e.EmailID, err)
		return err
	}

	log.Printf("[邮件内容保存] 成功保存邮件内容: ID=%d", e.EmailID)
	return nil
}
