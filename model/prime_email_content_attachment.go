package model

import (
	"go_email/db"
	"go_email/pkg/utils"
	"log"
	"time"

	"github.com/jinzhu/gorm"
)

// PrimeEmailContentAttachment 邮件附件表结构
type PrimeEmailContentAttachment struct {
	ID        uint           `gorm:"primary_key;column:id" json:"id"`
	EmailID   int            `gorm:"column:email_id" json:"email_id"`            // 邮件ID
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

// CreateWithTransaction 使用事务创建邮件附件记录
func (a *PrimeEmailContentAttachment) CreateWithTransaction(tx *gorm.DB) error {
	return tx.Create(a).Error
}

// BatchCreateAttachments 批量创建邮件附件记录
func BatchCreateAttachments(attachments []*PrimeEmailContentAttachment) error {
	return db.DB().Create(attachments).Error
}

// BatchCreateAttachmentsWithTransaction 使用事务批量创建邮件附件记录
func BatchCreateAttachmentsWithTransaction(tx *gorm.DB, attachments []*PrimeEmailContentAttachment) error {
	if len(attachments) == 0 {
		log.Println("[附件批量保存] 没有附件需要保存")
		return nil
	}

	// 记录要保存的每个附件的信息，并清理文件名
	for i, attachment := range attachments {
		// 清理文件名中的非法UTF-8字符
		attachment.FileName = utils.SanitizeUTF8(attachment.FileName)
		attachment.MimeType = utils.SanitizeUTF8(attachment.MimeType)
		attachment.OssUrl = utils.SanitizeUTF8(attachment.OssUrl)

		// 确保时间字段已初始化
		if attachment.CreatedAt.Time.IsZero() {
			attachment.CreatedAt = utils.JsonTime{Time: time.Now()}
		}
		if attachment.UpdatedAt.Time.IsZero() {
			attachment.UpdatedAt = utils.JsonTime{Time: time.Now()}
		}

		log.Printf("[附件批量保存] 准备保存附件 %d/%d: 邮件ID=%d, 文件名=%s, 大小=%.2f KB, 类型=%s",
			i+1, len(attachments), attachment.EmailID, attachment.FileName, attachment.SizeKb, attachment.MimeType)
	}

	// 使用单个Create而不是批量操作，避免反射问题
	for _, attachment := range attachments {
		if err := tx.Create(attachment).Error; err != nil {
			log.Printf("[附件批量保存] 保存附件失败: 邮件ID=%d, 文件名=%s, 错误=%v",
				attachment.EmailID, attachment.FileName, err)
			return err
		}
	}

	log.Printf("[附件批量保存] 成功批量保存 %d 个附件", len(attachments))
	return nil
}

// GetAttachmentsByIDs 根据ID列表获取附件
func GetAttachmentsByIDs(ids []uint) ([]*PrimeEmailContentAttachment, error) {
	var attachments []*PrimeEmailContentAttachment
	err := db.DB().Where("id IN (?)", ids).Find(&attachments).Error
	return attachments, err
}

// GetAttachmentsByEmailID 根据邮件ID获取附件列表
func GetAttachmentsByEmailID(emailID int) ([]*PrimeEmailContentAttachment, error) {
	var attachments []*PrimeEmailContentAttachment
	err := db.DB().Where("email_id = ?", emailID).Find(&attachments).Error
	return attachments, err
}
