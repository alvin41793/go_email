package model

import (
	"go_email/db"
	"time"
)

// PrimeEmailAccount 表示邮箱账号表结构
type PrimeEmailAccount struct {
	ID        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Account   string    `json:"account" gorm:"type:varchar(255)"`
	Password  string    `json:"password" gorm:"type:varchar(255)"`
	Status    int       `json:"status" gorm:"comment:'-1:删除 0:未启用 1:已启用'"`
	Type      int       `json:"type" gorm:"comment:'0:op账号'"`
	CreatedAt time.Time `json:"created_at" gorm:"type:datetime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"type:datetime"`
}

// GetActiveAccount 获取状态为启用的账号
func GetActiveAccount() ([]PrimeEmailAccount, error) {
	var account []PrimeEmailAccount
	result := db.DB().Where("status = ?", 1).Find(&account)
	return account, result.Error
}

func GetAccountByID(id int) (PrimeEmailAccount, error) {
	var account PrimeEmailAccount
	result := db.DB().Where("id = ?", id).First(&account)
	return account, result.Error
}
