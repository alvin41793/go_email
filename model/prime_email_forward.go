package model

import (
	"encoding/json"
	"go_email/db"
	"go_email/pkg/utils"
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
