package model

import (
	"go_email/db"
	"go_email/pkg/utils"
	"log"

	"gorm.io/gorm"
)

// PrimeEmail 邮件基本信息表结构
type PrimeEmail struct {
	ID            uint           `gorm:"primarykey;column:id" json:"id"`
	EmailID       int            `gorm:"column:email_id" json:"email_id"`
	AccountId     int            `gorm:"column:account_id" json:"account_id"`
	FromEmail     string         `gorm:"column:from_email;size:255" json:"from_email"` // 发送者
	Subject       string         `gorm:"column:subject;size:255" json:"subject"`       // 主题
	Date          string         `gorm:"column:date;size:255" json:"date"`             // 邮件日期
	HasAttachment int            `gorm:"column:has_attachment" json:"has_attachment"`  // 附件 0:没有 1:有
	Status        int            `gorm:"column:status" json:"status"`
	CreatedAt     utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// 清理邮件字段中的非法UTF-8字符
func sanitizeEmailFields(email *PrimeEmail) {
	// 确保所有文本字段都是有效的UTF-8
	if email.FromEmail != "" {
		email.FromEmail = utils.SanitizeUTF8(email.FromEmail)
	}
	if email.Subject != "" {
		email.Subject = utils.SanitizeUTF8(email.Subject)
	}
	if email.Date != "" {
		email.Date = utils.SanitizeUTF8(email.Date)
	}
}

// Create 创建一条邮件记录
func (e *PrimeEmail) Create() error {
	// 清理非法UTF-8字符
	sanitizeEmailFields(e)
	return db.DB().Create(e).Error
}

// BatchCreateEmails 批量创建邮件记录，如果邮件已存在则跳过
func BatchCreateEmails(emails []*PrimeEmail) error {
	if len(emails) == 0 {
		log.Println("[邮件列表] 没有新邮件需要保存")
		return nil
	}

	log.Printf("[邮件列表] 开始批量处理 %d 封邮件", len(emails))

	tx := db.DB().Begin()
	createdCount := 0
	skippedCount := 0

	for i, email := range emails {
		// 清理非法UTF-8字符
		sanitizeEmailFields(email)

		log.Printf("[邮件列表] 处理邮件 %d/%d: ID=%d, 主题=%s, 发件人=%s",
			i+1, len(emails), email.EmailID, email.Subject, email.FromEmail)

		// 使用GetEmailByEmailID检查邮件是否已存在
		existingEmail, err := GetEmailByEmailID(uint(email.EmailID))
		if existingEmail.ID > 0 && err == nil {
			// 邮件已存在，跳过此条记录
			log.Printf("[邮件列表] 邮件已存在，跳过: ID=%d", email.EmailID)
			skippedCount++
			continue
		} else if !db.IsRecordNotFoundError(err) {
			// 如果是查询出错而非记录不存在，则回滚并返回错误
			log.Printf("[邮件列表] 查询邮件是否存在时出错: ID=%d, 错误=%v", email.EmailID, err)
			tx.Rollback()
			return err
		}

		// 邮件不存在，创建新记录
		log.Printf("[邮件列表] 创建新邮件记录: ID=%d", email.EmailID)
		if err := tx.Create(email).Error; err != nil {
			log.Printf("[邮件列表] 创建邮件记录失败: ID=%d, 错误=%v", email.EmailID, err)
			tx.Rollback()
			return err
		}
		createdCount++
	}

	err := tx.Commit().Error
	if err != nil {
		log.Printf("[邮件列表] 提交事务失败: %v", err)
		return err
	}

	log.Printf("[邮件列表] 成功完成批量处理: 创建=%d, 跳过=%d, 总计=%d", createdCount, skippedCount, len(emails))
	return nil
}

// GetEmailByEmailID 根据EmailID获取邮件
func GetEmailByEmailID(emailId uint) (*PrimeEmail, error) {
	var email PrimeEmail
	err := db.DB().Where("email_id = ?", emailId).First(&email).Error
	return &email, err
}

// GetLatestEmail 获取最新的邮件记录
func GetLatestEmail() (PrimeEmail, error) {
	var email PrimeEmail
	err := db.DB().Order("email_id desc").First(&email).Error
	return email, err
}

// GetLatestEmailWithTx 使用事务获取最新的邮件记录
func GetLatestEmailWithTx(tx *gorm.DB) (PrimeEmail, error) {
	var email PrimeEmail
	err := tx.Order("email_id desc").First(&email).Error
	return email, err
}

// BatchCreateEmailsWithTx 使用事务批量创建邮件记录
func BatchCreateEmailsWithTx(emails []*PrimeEmail, tx *gorm.DB) error {
	if len(emails) == 0 {
		return nil
	}

	for _, email := range emails {
		if err := tx.Create(email).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetEmailByStatus 获取指定状态的邮件ID并更新其状态为"处理中"
func GetEmailByStatus(status, limit int) ([]PrimeEmail, error) {
	var emails []PrimeEmail

	// 开始事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 第一步：使用事务查询指定状态的邮件记录
	err := tx.Model(&PrimeEmail{}).
		Where("status = ?", status).
		Limit(limit).
		Find(&emails).Error

	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 如果没有找到邮件，直接返回
	if len(emails) == 0 {
		tx.Rollback() // 没有更新操作，回滚事务
		return emails, nil
	}

	// 获取所有email_id
	var emailIDs []int
	for _, email := range emails {
		emailIDs = append(emailIDs, email.EmailID)
	}

	// 第二步：更新这些邮件的状态为"处理中"(0)
	err = tx.Model(&PrimeEmail{}).
		Where("email_id IN (?)", emailIDs).
		Update("status", 0).Error

	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, err
	}

	return emails, nil
}

// ResetEmailStatus 重置邮件状态
func ResetEmailStatus(emailID int, status int) error {
	return db.DB().Model(&PrimeEmail{}).
		Where("email_id = ?", emailID).
		Update("status", status).Error
}
