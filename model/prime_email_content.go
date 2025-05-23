package model

import (
	"go_email/db"
	"go_email/pkg/utils"

	"github.com/jinzhu/gorm"
)

// PrimeEmailContent 邮件内容表结构
type PrimeEmailContent struct {
	ID          uint           `gorm:"primary_key;column:id" json:"id"`
	EmailID     int            `gorm:"column:email_id" json:"email_id"`
	Subject     string         `gorm:"column:subject;size:255" json:"subject"`            // 主题
	FromEmail   string         `gorm:"column:from_email;size:255" json:"from_email"`      // 发送者
	ToEmail     string         `gorm:"column:to_email;size:255" json:"to_email"`          // 接收者
	Date        string         `gorm:"column:date;size:255" json:"date"`                  // 邮件日期
	Content     string         `gorm:"column:content;type:text" json:"content"`           // 正文
	HTMLContent string         `gorm:"column:html_content;type:text" json:"html_content"` // html正文
	Type        int            `gorm:"column:type" json:"type"`                           // 邮件类型
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

// CreateWithTransaction 使用事务创建邮件内容
func (e *PrimeEmailContent) CreateWithTransaction(tx *gorm.DB) error {
	return tx.Create(e).Error
}
