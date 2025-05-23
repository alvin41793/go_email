package model

import (
	"go_email/db"
	"go_email/pkg/utils"
)

// PrimeEmailContentAttachment 邮件附件表结构
type PrimeEmailContentAttachment struct {
	ID        uint           `gorm:"primary_key;column:id" json:"id"`
	FileName  string         `gorm:"column:file_name;size:255" json:"file_name"` // 文件名
	SizeKb    float64        `gorm:"column:size_kb" json:"size_kb"`              // 文件大小
	MimeType  string         `gorm:"column:mime_type;size:255" json:"mime_type"` // 文件类型
	OssUrl    string         `gorm:"column:oss_url;size:255" json:"oss_url"`     // oss链接
	CreatedAt utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条邮件附件记录
func (a *PrimeEmailContentAttachment) Create() error {
	return db.DB().Create(a).Error
}

// BatchCreateAttachments 批量创建邮件附件记录
func BatchCreateAttachments(attachments []*PrimeEmailContentAttachment) error {
	return db.DB().Create(attachments).Error
}

// GetAttachmentsByIDs 根据ID列表获取附件
func GetAttachmentsByIDs(ids []uint) ([]*PrimeEmailContentAttachment, error) {
	var attachments []*PrimeEmailContentAttachment
	err := db.DB().Where("id IN (?)", ids).Find(&attachments).Error
	return attachments, err
}
