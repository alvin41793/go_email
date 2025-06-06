package model

import (
	"go_email/db"
	"go_email/pkg/utils"
)

// PrimeEmailIdentifyLog 邮件识别日志表结构
type PrimeEmailIdentifyLog struct {
	ID            uint           `gorm:"primarykey;column:id" json:"id"`
	EmailID       int            `gorm:"column:email_id" json:"email_id"`
	AccountId     int            `gorm:"column:account_id" json:"account_id"`
	BeginTime     utils.JsonTime `gorm:"column:begin_time" json:"begin_time"`
	EndTime       utils.JsonTime `gorm:"column:end_time" json:"end_time"`
	RunTime       int            `gorm:"column:run_time" json:"run_time"`
	Type          int            `gorm:"column:type" json:"type"`
	ResultStatus  int            `gorm:"column:result_status" json:"result_status"`
	ResultContent string         `gorm:"column:result_content;type:text" json:"result_content"`
	JsonContent   string         `gorm:"column:Json_content;type:text" json:"json_content"`
	CreatedAt     utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条邮件识别日志记录
func (e *PrimeEmailIdentifyLog) Create() error {
	return db.DB().Create(e).Error
}

// GetByID 根据ID获取邮件识别日志
func GetEmailIdentifyLogByID(id uint) (*PrimeEmailIdentifyLog, error) {
	var log PrimeEmailIdentifyLog
	err := db.DB().Where("id = ?", id).First(&log).Error
	return &log, err
}

// UpdateFields 更新指定字段
func (e *PrimeEmailIdentifyLog) UpdateFields(fields map[string]interface{}) error {
	return db.DB().Model(e).Updates(fields).Error
}
