package model

import (
	"encoding/json"
	"go_email/pkg/utils"
)

// PrimeEmailAnalysis 邮件分析表结构
type PrimeEmailAnalysis struct {
	ID           int             `gorm:"primarykey;column:id" json:"id"`
	EmailID      int             `gorm:"column:email_id" json:"email_id"`
	AccountId    int             `gorm:"column:account_id" json:"account_id"`
	ModelType    string          `gorm:"column:model_type;size:255" json:"model_type"` // 模型类型
	Mbl          string          `gorm:"column:mbl;size:255" json:"mbl"`               // MBL号
	Hbl          string          `gorm:"column:hbl;size:255" json:"hbl"`               // HBL号
	Container    json.RawMessage `gorm:"column:container;size:255" json:"container"`   // 集装箱号
	Confidence   float64         `gorm:"column:confidence" json:"confidence"`
	IsAttachment int             `gorm:"column:is_attachment" json:"is_attachment"`
	CreatedAt    utils.JsonTime  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    utils.JsonTime  `gorm:"column:updated_at" json:"updated_at"`
}
