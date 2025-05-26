package model

import (
	"go_email/db"
	"go_email/pkg/utils"
)

// PrimeEmailLog 邮件日志表结构
type PrimeEmailLog struct {
	ID             uint           `gorm:"primary_key;column:id" json:"id"`
	EmailID        int            `gorm:"column:email_id" json:"email_id"`
	EmailContent   string         `gorm:"column:email_content;type:text" json:"email_content"`
	EmailBeginTime utils.JsonTime `gorm:"column:email_begin_time" json:"email_begin_time"`
	BeginTime      utils.JsonTime `gorm:"column:begin_time" json:"begin_time"`
	EndTime        utils.JsonTime `gorm:"column:end_time" json:"end_time"`
	RunTime        int            `gorm:"column:run_time" json:"run_time"`
	ResultStatus   int            `gorm:"column:result_status" json:"result_status"`
	ResultContent  string         `gorm:"column:result_content;type:text" json:"result_content"`
	JsonContent    string         `gorm:"column:Json_content;type:text" json:"json_content"`
	SyncStatus     string         `gorm:"column:sync_status;size:255" json:"sync_status"`
	CreatedAt      utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条邮件日志记录
func (e *PrimeEmailLog) Create() error {
	return db.DB().Create(e).Error
}

// GetByID 根据ID获取邮件日志
func GetEmailLogByID(id uint) (*PrimeEmailLog, error) {
	var log PrimeEmailLog
	err := db.DB().Where("id = ?", id).First(&log).Error
	return &log, err
}

// GetByEmailID 根据EmailID获取邮件日志
func GetEmailLogByEmailID(emailId int) (*PrimeEmailLog, error) {
	var log PrimeEmailLog
	err := db.DB().Where("email_id = ?", emailId).First(&log).Error
	return &log, err
}

// UpdateFields 更新指定字段
func (e *PrimeEmailLog) UpdateFields(fields map[string]interface{}) error {
	return db.DB().Model(e).Updates(fields).Error
}

// ListByResultStatus 根据处理结果状态获取日志列表
func ListEmailLogsByResultStatus(resultStatus, limit int) ([]*PrimeEmailLog, error) {
	var logs []*PrimeEmailLog
	err := db.DB().Where("result_status = ?", resultStatus).Limit(limit).Find(&logs).Error
	return logs, err
}
