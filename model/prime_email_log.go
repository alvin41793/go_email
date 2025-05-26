package model

import (
	"go_email/db"
	"go_email/pkg/utils"
)

// PrimeEmailLog 邮件日志表结构
type PrimeEmailLog struct {
	ID           uint           `gorm:"primary_key;column:id" json:"id"`
	EmailID      int            `gorm:"column:email_id" json:"email_id"`
	EmailSubject string         `gorm:"column:email_subject;type:text" json:"email_subject"`
	BeginTime    utils.JsonTime `gorm:"column:begin_time" json:"begin_time"`
	EndTime      utils.JsonTime `gorm:"column:end_time" json:"end_time"`
	RunTime      int            `gorm:"column:run_time" json:"run_time"`
	ResultStatus int            `gorm:"column:result_status" json:"result_status"`
	CreatedAt    utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条邮件日志记录
func (e *PrimeEmailLog) Create() error {
	return db.DB().Create(e).Error
}

// GetEmailLogByID 根据ID获取邮件日志
func GetEmailLogByID(id uint) (*PrimeEmailLog, error) {
	var log PrimeEmailLog
	err := db.DB().Where("id = ?", id).First(&log).Error
	return &log, err
}

// GetEmailLogByEmailID 根据EmailID获取邮件日志
func GetEmailLogByEmailID(emailId int) (*PrimeEmailLog, error) {
	var log PrimeEmailLog
	err := db.DB().Where("email_id = ?", emailId).First(&log).Error
	return &log, err
}

// UpdateFields 更新指定字段
func (e *PrimeEmailLog) UpdateFields(fields map[string]interface{}) error {
	return db.DB().Model(e).Updates(fields).Error
}
