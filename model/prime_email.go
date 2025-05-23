package model

import (
	"go_email/db"
	"go_email/pkg/utils"
)

// PrimeEmail 邮件基本信息表结构
type PrimeEmail struct {
	ID            uint           `gorm:"primary_key;column:id" json:"id"`
	EmailID       int            `gorm:"column:email_id" json:"email_id"`
	FromEmail     string         `gorm:"column:from_email;size:255" json:"from_email"` // 发送者
	Subject       string         `gorm:"column:subject;size:255" json:"subject"`       // 主题
	Date          string         `gorm:"column:date;size:255" json:"date"`             // 邮件日期
	HasAttachment int            `gorm:"column:has_attachment" json:"has_attachment"`  // 附件 0:没有 1:有
	Status        int            `gorm:"column:status" json:"status"`
	CreatedAt     utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条邮件记录
func (e *PrimeEmail) Create() error {
	return db.DB().Create(e).Error
}

// BatchCreateEmails 批量创建邮件记录，如果邮件已存在则跳过
func BatchCreateEmails(emails []*PrimeEmail) error {
	tx := db.DB().Begin()
	for _, email := range emails {
		// 使用GetEmailByEmailID检查邮件是否已存在
		existingEmail, err := GetEmailByEmailID(uint(email.EmailID))
		if existingEmail.ID > 0 && err == nil {
			// 邮件已存在，跳过此条记录
			continue
		} else if !db.IsRecordNotFoundError(err) {
			// 如果是查询出错而非记录不存在，则回滚并返回错误
			tx.Rollback()
			return err
		}

		// 邮件不存在，创建新记录
		if err := tx.Create(email).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

// GetEmailByEmailID 根据EmailID获取邮件
func GetEmailByEmailID(emailId uint) (*PrimeEmail, error) {
	var email PrimeEmail
	err := db.DB().Where("email_id = ?", emailId).First(&email).Error
	return &email, err
}

func GetEmailByStatus(status, limit int) ([]int, error) {
	var emailIDs []int
	err := db.DB().Model(&PrimeEmail{}).Where("status = ?", status).Limit(limit).Pluck("email_id", &emailIDs).Error
	return emailIDs, err
}
