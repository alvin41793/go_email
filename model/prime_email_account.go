package model

import (
	"fmt"
	"go_email/db"
	"log"
	"math/rand"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PrimeEmailAccount 表示邮箱账号表结构
type PrimeEmailAccount struct {
	ID               int        `json:"id" gorm:"primaryKey;autoIncrement"`
	Account          string     `json:"account" gorm:"type:varchar(255)"`
	Password         string     `json:"password" gorm:"type:varchar(255)"`
	AppPassword      string     `json:"app_password" gorm:"type:varchar(255)"`
	Status           int        `json:"status" gorm:"comment:'-1:删除 0:未启用 1:已启用'"`
	Type             int        `json:"type" gorm:"comment:'0:op账号'"`
	Node             int        `json:"node" gorm:"type:int;default:1;comment:'节点编号，用于区分不同服务器'"`
	LastSyncTime     *time.Time `json:"last_sync_time" gorm:"type:datetime;comment:'最后同步时间'"`
	ProcessingStatus *int       `json:"processing_status" gorm:"type:int;default:0;comment:'处理状态: 0:空闲 1:处理中'"`
	CreatedAt        time.Time  `json:"created_at" gorm:"type:datetime"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"type:datetime"`
}

// GetAccountByID 根据ID获取账号信息
func GetAccountByID(id int) (PrimeEmailAccount, error) {
	var account PrimeEmailAccount
	result := db.DB().Where("id = ?", id).First(&account)
	return account, result.Error
}

// UpdateLastSyncTime 更新账号的最后同步时间
func UpdateLastSyncTime(accountID int) error {
	now := time.Now()
	result := db.DB().Model(&PrimeEmailAccount{}).Where("id = ?", accountID).Update("last_sync_time", now)
	return result.Error
}

// UpdateLastSyncTimeWithTx 使用事务更新账号的最后同步时间
func UpdateLastSyncTimeWithTx(tx *gorm.DB, accountID int) error {
	now := time.Now()
	result := tx.Model(&PrimeEmailAccount{}).Where("id = ?", accountID).Update("last_sync_time", now)
	return result.Error
}

// CleanupStuckProcessingAccounts 清理卡死的处理状态账号
func CleanupStuckProcessingAccounts(timeoutMinutes int, node int) (int, error) {
	timeoutThreshold := time.Now().Add(-time.Duration(timeoutMinutes) * time.Minute)
	database := db.DB().Model(&PrimeEmailAccount{})

	whereCondition := "processing_status = 1 AND (last_sync_time < ? OR last_sync_time IS NULL)"
	args := []interface{}{timeoutThreshold}

	if node > 0 {
		whereCondition += " AND node = ?"
		args = append(args, node)
	}

	var stuckAccounts []PrimeEmailAccount
	if err := database.Where(whereCondition, args...).Find(&stuckAccounts).Error; err != nil {
		return 0, err
	}

	if len(stuckAccounts) == 0 {
		log.Printf("[状态清理] 没有发现卡死的账号")
		return 0, nil
	}

	var accountIDs []int
	for _, account := range stuckAccounts {
		accountIDs = append(accountIDs, account.ID)
		log.Printf("[状态清理] 发现卡死账号: ID=%d, Account=%s, Node=%d",
			account.ID, account.Account, account.Node)
	}

	updates := map[string]interface{}{
		"processing_status": 0,
		"last_sync_time":    time.Now().Add(-2 * time.Hour),
	}

	result := database.Where("id IN (?)", accountIDs).Updates(updates)
	if result.Error != nil {
		return 0, result.Error
	}

	cleanedCount := int(result.RowsAffected)
	log.Printf("[状态清理] 成功重置 %d 个卡死账号的状态", cleanedCount)
	return cleanedCount, nil
}

// 以下是兼容性函数，用于兼容原有代码中的函数调用

// GetActiveAccountByContentSyncTime 获取状态为启用的账号，按最后同步时间排序（兼容性函数）
func GetActiveAccountByContentSyncTime(limit int) ([]PrimeEmailAccount, error) {
	var accounts []PrimeEmailAccount
	result := db.DB().Where("status = ?", 1).Order("ISNULL(last_sync_time) DESC, last_sync_time ASC").Limit(limit).Find(&accounts)
	return accounts, result.Error
}

// GetAndUpdateAccountsForContent 原子性地获取账号并更新同步时间，防止并发竞争（兼容性函数）
func GetAndUpdateAccountsForContent(node int, limit int) ([]PrimeEmailAccount, error) {
	return GetAndUpdateAccountsForUnifiedSync(node, limit)
}

// UpdateLastSyncContentTimeOnComplete 在账号处理完成后更新真正的同步时间（兼容性函数）
func UpdateLastSyncContentTimeOnComplete(accountID int) error {
	return UpdateLastSyncTimeOnComplete(accountID)
}

// ResetSyncContentTimeOnFailure 在账号处理失败后重置同步时间（兼容性函数）
func ResetSyncContentTimeOnFailure(accountID int) error {
	return ResetSyncTimeOnFailure(accountID)
}

// GetAndUpdateAccountsForUnifiedSync 原子性地获取账号并更新同步时间，用于统一同步
func GetAndUpdateAccountsForUnifiedSync(node int, limit int) ([]PrimeEmailAccount, error) {
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		accounts, err := getAndUpdateAccountsForUnifiedSyncOnce(node, limit)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "deadlock") {
				if attempt < maxRetries {
					waitTime := time.Duration(50+rand.Intn(100)) * time.Millisecond
					log.Printf("[统一同步] 检测到死锁，第 %d/%d 次重试，等待 %v 后重试",
						attempt, maxRetries, waitTime)
					time.Sleep(waitTime)
					continue
				}
				log.Printf("[统一同步] 死锁重试失败，已达到最大重试次数: %d", maxRetries)
			}
			return nil, err
		}
		return accounts, nil
	}
	return nil, fmt.Errorf("获取账号失败，已达到最大重试次数")
}

// getAndUpdateAccountsForUnifiedSyncOnce 单次执行获取和更新账号的操作
func getAndUpdateAccountsForUnifiedSyncOnce(node int, limit int) ([]PrimeEmailAccount, error) {
	var accounts []PrimeEmailAccount

	tx := db.DB().Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	var result *gorm.DB
	if node > 0 {
		result = tx.Set("gorm:query_option", "FOR UPDATE").
			Where("status = ? AND node = ? AND (processing_status IS NULL OR processing_status = 0)", 1, node).
			Order("id ASC, ISNULL(last_sync_time) DESC, last_sync_time ASC").
			Limit(limit).
			Find(&accounts)
	} else {
		result = tx.Set("gorm:query_option", "FOR UPDATE").
			Where("status = ? AND (processing_status IS NULL OR processing_status = 0)", 1).
			Order("id ASC, ISNULL(last_sync_time) DESC, last_sync_time ASC").
			Limit(limit).
			Find(&accounts)
	}

	if result.Error != nil {
		tx.Rollback()
		return nil, result.Error
	}

	if len(accounts) == 0 {
		tx.Rollback()
		return accounts, nil
	}

	now := time.Now()
	accountIDs := make([]int, len(accounts))
	for i, account := range accounts {
		accountIDs[i] = account.ID
	}

	if err := tx.Model(&PrimeEmailAccount{}).
		Where("id IN (?)", accountIDs).
		Updates(map[string]interface{}{
			"last_sync_time":    now,
			"processing_status": 1,
		}).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	log.Printf("[统一同步] 成功批量更新 %d 个账号状态", len(accounts))
	return accounts, nil
}

// UpdateLastSyncTimeOnComplete 在账号处理完成后更新真正的同步时间
func UpdateLastSyncTimeOnComplete(accountID int) error {
	now := time.Now()
	result := db.DB().Model(&PrimeEmailAccount{}).
		Where("id = ?", accountID).
		Updates(map[string]interface{}{
			"last_sync_time":    now,
			"processing_status": 0,
		})
	return result.Error
}

// ResetSyncTimeOnFailure 在账号处理失败后重置同步时间（让其能被重新优先选择）
func ResetSyncTimeOnFailure(accountID int) error {
	resetTime := time.Now().Add(-24 * time.Hour)
	result := db.DB().Model(&PrimeEmailAccount{}).
		Where("id = ?", accountID).
		Updates(map[string]interface{}{
			"last_sync_time":    resetTime,
			"processing_status": 0,
		})
	return result.Error
}
