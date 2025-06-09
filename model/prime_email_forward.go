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
// 返回记录列表和错误信息
func GetAndUpdatePendingForwards(limit int) ([]PrimeEmailForward, error) {
	var records []PrimeEmailForward
	tx := db.DB().Begin()

	// 确保事务会被适当处理
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 查询前limit条状态为-1的记录
	if err := tx.Where("status = ?", -1).Limit(limit).Find(&records).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 如果没有找到记录，提交事务并返回空结果
	if len(records) == 0 {
		tx.Commit()
		return records, nil
	}

	// 更新这些记录的状态为处理中(0)
	var ids []int
	for _, record := range records {
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

	return records, nil
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
