package model

import (
	"fmt"
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
func GetLatestEmailWithTx(tx *gorm.DB, accountId int) (PrimeEmail, error) {
	var email PrimeEmail
	err := tx.Where("account_id=?", accountId).Order("email_id desc").First(&email).Error
	return email, err
}

// BatchCreateEmailsWithTx 使用事务批量创建邮件记录，支持容错处理
func BatchCreateEmailsWithTx(emails []*PrimeEmail, tx *gorm.DB) error {
	if len(emails) == 0 {
		return nil
	}

	successCount := 0
	failCount := 0
	var failedEmails []string

	for _, email := range emails {
		// 先检查是否已存在相同的email_id和account_id记录
		var count int64
		if err := tx.Model(&PrimeEmail{}).
			Where("email_id = ? AND account_id = ?", email.EmailID, email.AccountId).
			Count(&count).Error; err != nil {
			log.Printf("[邮件批量插入] 检查记录是否存在时出错: email_id=%d, account_id=%d, 错误=%v",
				email.EmailID, email.AccountId, err)
			failCount++
			failedEmails = append(failedEmails, fmt.Sprintf("email_id=%d(检查失败)", email.EmailID))
			continue // 跳过这条记录，继续处理下一条
		}

		// 如果记录已存在，则跳过此条记录的创建
		if count > 0 {
			log.Printf("[邮件批量插入] 记录已存在，跳过: email_id=%d, account_id=%d", email.EmailID, email.AccountId)
			continue
		}

		// 记录不存在，创建新记录
		if err := tx.Create(email).Error; err != nil {
			log.Printf("[邮件批量插入] 创建记录失败，跳过: email_id=%d, account_id=%d, 错误=%v",
				email.EmailID, email.AccountId, err)
			failCount++
			failedEmails = append(failedEmails, fmt.Sprintf("email_id=%d(插入失败)", email.EmailID))
			continue // 跳过这条记录，继续处理下一条
		}

		successCount++
	}

	log.Printf("[邮件批量插入] 批量处理完成: 成功=%d, 失败=%d, 总计=%d",
		successCount, failCount, len(emails))

	if failCount > 0 {
		log.Printf("[邮件批量插入] 失败的记录: %v", failedEmails)
	}

	return nil
}

// GetEmailByStatusAndNode 获取指定状态和指定节点的邮件ID并更新其状态为"处理中"，平均分配给不同的AccountId
func GetEmailByStatusAndNode(status, limit, node int) ([]PrimeEmail, error) {
	var emails []PrimeEmail

	// 检查节点参数是否有效
	if node <= 0 {
		return nil, fmt.Errorf("节点编号必须大于0，当前值: %d", node)
	}

	// 开始事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 第一步：先查询指定节点下的所有活跃账号ID（性能优化：避免大表JOIN）
	var nodeAccountIds []int
	err := tx.Model(&PrimeEmailAccount{}).
		Where("node = ? AND status = 1", node).
		Pluck("id", &nodeAccountIds).Error

	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 如果该节点下没有活跃账号，直接返回
	if len(nodeAccountIds) == 0 {
		tx.Rollback()
		log.Printf("[邮件分配] 节点 %d 下没有找到活跃账号", node)
		return emails, nil
	}

	// 第二步：查询这些账号中有指定状态邮件的账号ID
	var accountIds []int
	err = tx.Model(&PrimeEmail{}).
		Where("status = ? AND account_id IN (?)", status, nodeAccountIds).
		Distinct("account_id").
		Pluck("account_id", &accountIds).Error

	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 如果没有找到任何有该状态邮件的账户，直接返回
	if len(accountIds) == 0 {
		tx.Rollback()
		log.Printf("[邮件分配] 节点 %d 下没有找到状态为 %d 的邮件", node, status)
		return emails, nil
	}

	// 第二步：计算每个AccountId应该分配的数量
	perAccountLimit := limit / len(accountIds)
	remainder := limit % len(accountIds)

	log.Printf("[邮件分配] 节点 %d - 总限制: %d, 账户数量: %d, 每账户基础分配: %d, 余数: %d",
		node, limit, len(accountIds), perAccountLimit, remainder)

	var allEmailIDs []int

	// 第三步：对每个AccountId分别查询相应数量的记录
	for i, accountId := range accountIds {
		var accountEmails []PrimeEmail
		currentLimit := perAccountLimit

		// 将余数分配给前几个账户
		if i < remainder {
			currentLimit++
		}

		// 如果当前账户分配数量为0，跳过
		if currentLimit == 0 {
			continue
		}

		err := tx.Model(&PrimeEmail{}).
			Where("status = ? AND account_id = ?", status, accountId).
			Limit(currentLimit).
			Find(&accountEmails).Error

		if err != nil {
			tx.Rollback()
			return nil, err
		}

		log.Printf("[邮件分配] 节点 %d - 账户ID %d 分配到 %d 封邮件", node, accountId, len(accountEmails))

		// 将此账户的邮件添加到总结果中
		emails = append(emails, accountEmails...)

		// 收集email_id用于后续状态更新
		for _, email := range accountEmails {
			allEmailIDs = append(allEmailIDs, email.EmailID)
		}
	}

	// 如果没有找到邮件，直接返回
	if len(emails) == 0 {
		tx.Rollback()
		log.Printf("[邮件分配] 节点 %d 下没有找到需要处理的邮件", node)
		return emails, nil
	}

	// 第四步：更新这些邮件的状态为"处理中"(0)
	err = tx.Model(&PrimeEmail{}).
		Where("email_id IN (?)", allEmailIDs).
		Update("status", 0).Error

	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, err
	}

	log.Printf("[邮件分配] 节点 %d - 成功分配 %d 封邮件给 %d 个账户", node, len(emails), len(accountIds))
	return emails, nil
}

// ResetEmailStatus 重置邮件状态
func ResetEmailStatus(emailID int, status int) error {
	return db.DB().Model(&PrimeEmail{}).
		Where("email_id = ?", emailID).
		Update("status", status).Error
}

// BatchCreateResult 批量创建结果统计
type BatchCreateResult struct {
	TotalCount   int      `json:"total_count"`   // 总记录数
	SuccessCount int      `json:"success_count"` // 成功插入数
	SkippedCount int      `json:"skipped_count"` // 跳过数（已存在）
	FailedCount  int      `json:"failed_count"`  // 失败数
	FailedEmails []string `json:"failed_emails"` // 失败的邮件ID列表
}

// BatchCreateEmailsWithStats 使用事务批量创建邮件记录，返回详细统计信息
func BatchCreateEmailsWithStats(emails []*PrimeEmail, tx *gorm.DB) (*BatchCreateResult, error) {
	result := &BatchCreateResult{
		TotalCount:   len(emails),
		SuccessCount: 0,
		SkippedCount: 0,
		FailedCount:  0,
		FailedEmails: make([]string, 0),
	}

	if len(emails) == 0 {
		return result, nil
	}

	for _, email := range emails {
		// 先检查是否已存在相同的email_id和account_id记录
		var count int64
		if err := tx.Model(&PrimeEmail{}).
			Where("email_id = ? AND account_id = ?", email.EmailID, email.AccountId).
			Count(&count).Error; err != nil {
			log.Printf("[邮件批量插入] 检查记录是否存在时出错: email_id=%d, account_id=%d, 错误=%v",
				email.EmailID, email.AccountId, err)
			result.FailedCount++
			result.FailedEmails = append(result.FailedEmails, fmt.Sprintf("email_id=%d(检查失败:%v)", email.EmailID, err))
			continue // 跳过这条记录，继续处理下一条
		}

		// 如果记录已存在，则跳过此条记录的创建
		if count > 0 {
			log.Printf("[邮件批量插入] 记录已存在，跳过: email_id=%d, account_id=%d", email.EmailID, email.AccountId)
			result.SkippedCount++
			continue
		}

		// 记录不存在，创建新记录
		if err := tx.Create(email).Error; err != nil {
			log.Printf("[邮件批量插入] 创建记录失败，跳过: email_id=%d, account_id=%d, 错误=%v",
				email.EmailID, email.AccountId, err)
			result.FailedCount++
			result.FailedEmails = append(result.FailedEmails, fmt.Sprintf("email_id=%d(插入失败:%v)", email.EmailID, err))
			continue // 跳过这条记录，继续处理下一条
		}

		result.SuccessCount++
	}

	log.Printf("[邮件批量插入] 批量处理完成: 总计=%d, 成功=%d, 跳过=%d, 失败=%d",
		result.TotalCount, result.SuccessCount, result.SkippedCount, result.FailedCount)

	if result.FailedCount > 0 {
		log.Printf("[邮件批量插入] 失败的记录: %v", result.FailedEmails)
	}

	return result, nil
}

// GetEmailByStatusAndAccount 获取特定账号的指定状态邮件并更新状态为"处理中"
func GetEmailByStatusAndAccount(status int, accountID int, limit int) ([]PrimeEmail, error) {
	var emails []PrimeEmail

	// 开始事务
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 查询指定账号的指定状态邮件
	err := tx.Model(&PrimeEmail{}).
		Where("status = ? AND account_id = ?", status, accountID).
		Limit(limit).
		Find(&emails).Error

	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 如果没有找到邮件，直接返回
	if len(emails) == 0 {
		tx.Rollback()
		return emails, nil
	}

	// 收集email_id用于状态更新
	var emailIDs []int
	for _, email := range emails {
		emailIDs = append(emailIDs, email.EmailID)
	}

	// 更新这些邮件的状态为"处理中"(0)
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

	log.Printf("[邮件分配] 账号ID %d - 成功分配 %d 封邮件", accountID, len(emails))
	return emails, nil
}
