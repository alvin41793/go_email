package model

import (
	"go_email/db"
	"time"

	"gorm.io/gorm"
)

// PrimeEmailAccount 表示邮箱账号表结构
type PrimeEmailAccount struct {
	ID                  int        `json:"id" gorm:"primaryKey;autoIncrement"`
	Account             string     `json:"account" gorm:"type:varchar(255)"`
	Password            string     `json:"password" gorm:"type:varchar(255)"`
	AppPassword         string     `json:"app_password" gorm:"type:varchar(255)"`
	Status              int        `json:"status" gorm:"comment:'-1:删除 0:未启用 1:已启用'"`
	Type                int        `json:"type" gorm:"comment:'0:op账号'"`
	Node                int        `json:"node" gorm:"type:int;default:1;comment:'节点编号，用于区分不同服务器'"`
	LastSyncListTime    *time.Time `json:"last_sync_list_time" gorm:"type:datetime;comment:'最后同步时间'"`
	LastSyncContentTime *time.Time `json:"last_sync_content_time" gorm:"type:datetime;comment:'最后同步时间'"`
	CreatedAt           time.Time  `json:"created_at" gorm:"type:datetime"`
	UpdatedAt           time.Time  `json:"updated_at" gorm:"type:datetime"`
}

// GetActiveAccount 获取状态为启用的账号，按最后同步时间排序（优先处理最久未同步的账户）
func GetActiveAccount() ([]PrimeEmailAccount, error) {
	var account []PrimeEmailAccount
	// 按last_sync_time升序排列，NULL值排在最前面（从未同步的账户优先）
	result := db.DB().Where("status = ?", 1).Order("ISNULL(last_sync_list_time) DESC, last_sync_list_time ASC").Find(&account)
	return account, result.Error
}

// GetActiveAccountByNode 根据节点编号获取状态为启用的账号，按最后同步时间排序
func GetActiveAccountByNode(node int) ([]PrimeEmailAccount, error) {
	var account []PrimeEmailAccount
	// 按node和last_sync_time筛选排序
	result := db.DB().Where("status = ? AND node = ?", 1, node).Order("ISNULL(last_sync_list_time) DESC, last_sync_list_time ASC").Find(&account)

	return account, result.Error
}

func GetAccountByID(id int) (PrimeEmailAccount, error) {
	var account PrimeEmailAccount
	result := db.DB().Where("id = ?", id).First(&account)
	return account, result.Error
}

// UpdateLastSyncTime 更新账号的最后同步时间
func UpdateLastSyncTime(accountID int) error {
	now := time.Now()
	result := db.DB().Model(&PrimeEmailAccount{}).Where("id = ?", accountID).Update("last_sync_list_time", now)
	return result.Error
}

// UpdateLastSyncTimeWithTx 使用事务更新账号的最后同步时间
func UpdateLastSyncTimeWithTx(tx *gorm.DB, accountID int) error {
	now := time.Now()
	result := tx.Model(&PrimeEmailAccount{}).Where("id = ?", accountID).Update("last_sync_list_time", now)
	return result.Error
}

// GetActiveAccountByContentSyncTime 获取状态为启用的账号，按最后同步邮件内容时间排序（优先处理最久未同步的账户）
func GetActiveAccountByContentSyncTime(limit int) ([]PrimeEmailAccount, error) {
	var accounts []PrimeEmailAccount
	// 按last_sync_content_time升序排列，NULL值排在最前面（从未同步的账户优先）
	result := db.DB().Where("status = ?", 1).Order("ISNULL(last_sync_content_time) DESC, last_sync_content_time ASC").Limit(limit).Find(&accounts)
	return accounts, result.Error
}

// GetActiveAccountByContentSyncTimeAndNode 根据节点编号获取状态为启用的账号，按最后同步邮件内容时间排序
func GetActiveAccountByContentSyncTimeAndNode(node int, limit int) ([]PrimeEmailAccount, error) {
	var accounts []PrimeEmailAccount
	// 按node和last_sync_content_time筛选排序
	result := db.DB().Where("status = ? AND node = ?", 1, node).Order("ISNULL(last_sync_content_time) DESC, last_sync_content_time ASC").Limit(limit).Find(&accounts)
	return accounts, result.Error
}

// UpdateLastSyncContentTime 更新账号的最后同步邮件内容时间
func UpdateLastSyncContentTime(accountID int) error {
	now := time.Now()
	result := db.DB().Model(&PrimeEmailAccount{}).Where("id = ?", accountID).Update("last_sync_content_time", now)
	return result.Error
}

// UpdateLastSyncContentTimeWithTx 使用事务更新账号的最后同步邮件内容时间
func UpdateLastSyncContentTimeWithTx(tx *gorm.DB, accountID int) error {
	now := time.Now()
	result := tx.Model(&PrimeEmailAccount{}).Where("id = ?", accountID).Update("last_sync_content_time", now)
	return result.Error
}
