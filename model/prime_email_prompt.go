package model

import (
	"go_email/db"
	"go_email/pkg/utils"
)

// PrimeEmailPrompt 邮件提示词表结构
type PrimeEmailPrompt struct {
	ID          uint           `gorm:"primarykey;column:id" json:"id"`
	EmailPrompt string         `gorm:"column:email_prompt;type:text" json:"email_prompt"`
	PdfPrompt   string         `gorm:"column:pdf_prompt;type:text" json:"pdf_prompt"`
	Type        int            `gorm:"column:type" json:"type"`
	Status      int            `gorm:"column:status" json:"status"`
	CreatedAt   utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条提示词记录
func (p *PrimeEmailPrompt) Create() error {
	return db.DB().Create(p).Error
}

// GetByID 根据ID获取提示词
func GetPromptByID(id uint) (*PrimeEmailPrompt, error) {
	var prompt PrimeEmailPrompt
	err := db.DB().Where("id = ?", id).First(&prompt).Error
	return &prompt, err
}

// UpdateFields 更新指定字段
func (p *PrimeEmailPrompt) UpdateFields(fields map[string]interface{}) error {
	return db.DB().Model(p).Updates(fields).Error
}

// ListActivePrompts 获取所有活跃的提示词
func ListActivePrompts() ([]*PrimeEmailPrompt, error) {
	var prompts []*PrimeEmailPrompt
	err := db.DB().Where("status = 1").Find(&prompts).Error
	return prompts, err
}

// ChangeStatus 更改提示词状态
func (p *PrimeEmailPrompt) ChangeStatus(status int) error {
	return db.DB().Model(p).Update("status", status).Error
}
