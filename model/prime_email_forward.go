package model

import (
	"encoding/json"
	"fmt"
	"go_email/db"
	"go_email/pkg/utils"
	"strings"

	"gorm.io/gorm"
)

// PrimeEmailForward 邮件转发表结构
type PrimeEmailForward struct {
	ID            int             `json:"id"`
	ForwardRuleId int             `json:"forward_rule_Id"`
	EmailID       int             `json:"email_id" gorm:"type:int"`
	AccountId     int             `gorm:"column:account_id" json:"account_id"`
	PrimeOp       string          `json:"prime_op" gorm:"type:varchar(255)"`
	Mbl           string          `json:"mbl" gorm:"type:varchar(255)"`
	Hbl           string          `json:"hbl" gorm:"type:varchar(255)"`
	Container     json.RawMessage `json:"container" gorm:"type:json"`
	Confidence    float64         `json:"confidence"`
	Type          int             `json:"type"`
	Status        int             `json:"status"`
	ResultContent string          `json:"result_content" gorm:"type:text"`
	CreatedAt     utils.JsonTime  `json:"created_at" gorm:"type:datetime"`
	UpdatedAt     utils.JsonTime  `json:"updated_at" gorm:"type:datetime"`
}

// ContainerInfo 集装箱信息结构
type ContainerInfo struct {
	Size        string `json:"size"`
	ContainerNo string `json:"container_no"`
}

// GetExistingForward 检查同一个email_id和prime_op是否已存在
func GetExistingForward(emailID int, primeOp string) (*PrimeEmailForward, error) {
	var forward PrimeEmailForward
	result := db.DB().Where("email_id = ? AND prime_op = ?", emailID, primeOp).First(&forward)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return nil, result.Error
	}
	return &forward, nil
}

// GetAndUpdatePendingForwards 获取待转发记录并更新状态为处理中
// 根据不同的account_id平均分配limit数量
// 返回记录列表和错误信息
func GetAndUpdatePendingForwards(limit int) ([]PrimeEmailForward, error) {
	var allRecords []PrimeEmailForward
	tx := db.DB().Begin()

	// 确保事务会被适当处理
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 首先查询有哪些不同的account_id（状态为-1的记录）
	var accountIDs []int
	if err := tx.Model(&PrimeEmailForward{}).
		Where("status = ?", -1).
		Distinct("account_id").
		Pluck("account_id", &accountIDs).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 如果没有找到任何account_id，提交事务并返回空结果
	if len(accountIDs) == 0 {
		tx.Commit()
		return allRecords, nil
	}

	// 计算每个account_id应该分配的记录数量
	limitPerAccount := limit / len(accountIDs)
	remainder := limit % len(accountIDs)

	// 按account_id分别查询记录
	for i, accountID := range accountIDs {
		var records []PrimeEmailForward
		currentLimit := limitPerAccount

		// 将余数分配给前面的几个account_id
		if i < remainder {
			currentLimit++
		}

		// 查询当前account_id的记录
		if err := tx.Where("status = ? AND account_id = ?", -1, accountID).
			Limit(currentLimit).
			Find(&records).Error; err != nil {
			tx.Rollback()
			return nil, err
		}

		// 将记录添加到总列表中
		allRecords = append(allRecords, records...)
	}

	// 如果没有找到任何记录，提交事务并返回空结果
	if len(allRecords) == 0 {
		tx.Commit()
		return allRecords, nil
	}

	// 更新这些记录的状态为处理中(0)
	var ids []int
	for _, record := range allRecords {
		ids = append(ids, record.ID)
	}

	if err := tx.Model(&PrimeEmailForward{}).Where("id IN ?", ids).Update("status", 0).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	return allRecords, nil
}

// UpdateForwardFailureStatus 更新转发失败状态和错误信息
func UpdateForwardFailureStatus(id int, err error) error {
	// 构造错误信息的JSON字符串
	resultContent := "{\"error\": \"" + strings.Replace(fmt.Sprintf("转发邮件失败: %v", err), "\"", "\\\"", -1) + "\"}"

	// 更新状态为失败(-1)和结果内容
	return db.DB().Model(&PrimeEmailForward{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":         -1,
		"result_content": resultContent,
	}).Error
}

// UpdateForwardSuccessStatus 更新转发成功状态
func UpdateForwardSuccessStatus(id int) error {
	// 构造成功信息的JSON字符串
	resultContent := "{\"success\": \"转发邮件成功\"}"

	// 更新状态为成功(1)和结果内容
	return db.DB().Model(&PrimeEmailForward{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":         1,
		"result_content": resultContent,
	}).Error
}
