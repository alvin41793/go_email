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
	ID                  int        `json:"id" gorm:"primaryKey;autoIncrement"`
	Account             string     `json:"account" gorm:"type:varchar(255)"`
	Password            string     `json:"password" gorm:"type:varchar(255)"`
	AppPassword         string     `json:"app_password" gorm:"type:varchar(255)"`
	Status              int        `json:"status" gorm:"comment:'-1:删除 0:未启用 1:已启用'"`
	Type                int        `json:"type" gorm:"comment:'0:op账号'"`
	Node                int        `json:"node" gorm:"type:int;default:1;comment:'节点编号，用于区分不同服务器'"`
	LastSyncListTime    *time.Time `json:"last_sync_list_time" gorm:"type:datetime;comment:'最后同步时间'"`
	LastSyncContentTime *time.Time `json:"last_sync_content_time" gorm:"type:datetime;comment:'最后同步时间'"`
	ProcessingStatus    *int       `json:"processing_status" gorm:"type:int;default:0;comment:'处理状态: 0:空闲 1:处理中'"`
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

// GetAndUpdateAccountsForContent 原子性地获取账号并更新同步时间，防止并发竞争
func GetAndUpdateAccountsForContent(node int, limit int) ([]PrimeEmailAccount, error) {
	// 添加死锁重试机制，最多重试3次
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		accounts, err := getAndUpdateAccountsForContentOnce(node, limit)
		if err != nil {
			// 检查是否是死锁错误
			if strings.Contains(strings.ToLower(err.Error()), "deadlock") {
				if attempt < maxRetries {
					// 随机等待一段时间后重试，避免多个请求同时重试
					waitTime := time.Duration(50+rand.Intn(100)) * time.Millisecond
					log.Printf("[邮件处理] 检测到死锁，第 %d/%d 次重试，等待 %v 后重试",
						attempt, maxRetries, waitTime)
					time.Sleep(waitTime)
					continue
				}
				log.Printf("[邮件处理] 死锁重试失败，已达到最大重试次数: %d", maxRetries)
			}
			return nil, err
		}
		return accounts, nil
	}
	return nil, fmt.Errorf("获取账号失败，已达到最大重试次数")
}

// getAndUpdateAccountsForContentOnce 单次执行获取和更新账号的操作
func getAndUpdateAccountsForContentOnce(node int, limit int) ([]PrimeEmailAccount, error) {
	var accounts []PrimeEmailAccount

	// 使用事务确保原子性
	tx := db.DB().Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 使用 SELECT ... FOR UPDATE 行锁来防止并发读取相同记录
	// 添加 ORDER BY id 确保锁的获取顺序一致，减少死锁概率
	var result *gorm.DB
	if node > 0 {
		result = tx.Set("gorm:query_option", "FOR UPDATE").
			Where("status = ? AND node = ? AND (processing_status IS NULL OR processing_status = 0)", 1, node).
			Order("id ASC, ISNULL(last_sync_content_time) DESC, last_sync_content_time ASC").
			Limit(limit).
			Find(&accounts)
	} else {
		result = tx.Set("gorm:query_option", "FOR UPDATE").
			Where("status = ? AND (processing_status IS NULL OR processing_status = 0)", 1).
			Order("id ASC, ISNULL(last_sync_content_time) DESC, last_sync_content_time ASC").
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

	// 【优化】使用批量更新而不是循环更新，减少锁持有时间
	now := time.Now()
	accountIDs := make([]int, len(accounts))
	for i, account := range accounts {
		accountIDs[i] = account.ID
	}

	// 批量更新所有选中的账号
	if err := tx.Model(&PrimeEmailAccount{}).
		Where("id IN (?)", accountIDs).
		Updates(map[string]interface{}{
			"last_sync_content_time": now,
			"processing_status":      1, // 标记为处理中
		}).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	log.Printf("[邮件处理] 成功批量更新 %d 个账号状态", len(accounts))
	return accounts, nil
}

// UpdateLastSyncContentTimeOnComplete 在账号处理完成后更新真正的同步时间
func UpdateLastSyncContentTimeOnComplete(accountID int) error {
	now := time.Now()
	result := db.DB().Model(&PrimeEmailAccount{}).
		Where("id = ?", accountID).
		Updates(map[string]interface{}{
			"last_sync_content_time": now,
			"processing_status":      0, // 标记为空闲
		})
	return result.Error
}

// ResetSyncContentTimeOnFailure 在账号处理失败后，将同步时间重置为较早的时间，让它能够被重新选择
func ResetSyncContentTimeOnFailure(accountID int) error {
	// 将失败的账号的同步时间设置为1小时前，这样它会在下次请求时被优先选择
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	result := db.DB().Model(&PrimeEmailAccount{}).
		Where("id = ?", accountID).
		Updates(map[string]interface{}{
			"last_sync_content_time": oneHourAgo,
			"processing_status":      0, // 标记为空闲
		})
	return result.Error
}

// GetAndUpdateAccountsForList 原子性地获取账号并更新同步时间，防止邮件列表同步的并发竞争
func GetAndUpdateAccountsForList(node int, limit int) ([]PrimeEmailAccount, error) {
	// 添加死锁重试机制，最多重试3次
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		accounts, err := getAndUpdateAccountsForListOnce(node, limit)
		if err != nil {
			// 检查是否是死锁错误
			if strings.Contains(strings.ToLower(err.Error()), "deadlock") {
				if attempt < maxRetries {
					// 随机等待一段时间后重试，避免多个请求同时重试
					waitTime := time.Duration(50+rand.Intn(100)) * time.Millisecond
					log.Printf("[邮件同步] 检测到死锁，第 %d/%d 次重试，等待 %v 后重试",
						attempt, maxRetries, waitTime)
					time.Sleep(waitTime)
					continue
				}
				log.Printf("[邮件同步] 死锁重试失败，已达到最大重试次数: %d", maxRetries)
			}
			return nil, err
		}
		return accounts, nil
	}
	return nil, fmt.Errorf("获取账号失败，已达到最大重试次数")
}

// getAndUpdateAccountsForListOnce 单次执行获取和更新账号的操作
func getAndUpdateAccountsForListOnce(node int, limit int) ([]PrimeEmailAccount, error) {
	var accounts []PrimeEmailAccount

	// 使用事务确保原子性
	tx := db.DB().Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 使用 SELECT ... FOR UPDATE 行锁来防止并发读取相同记录
	// 添加 ORDER BY id 确保锁的获取顺序一致，减少死锁概率
	var result *gorm.DB
	if node > 0 {
		result = tx.Set("gorm:query_option", "FOR UPDATE").
			Where("status = ? AND node = ? AND (processing_status IS NULL OR processing_status = 0)", 1, node).
			Order("id ASC, ISNULL(last_sync_list_time) DESC, last_sync_list_time ASC").
			Limit(limit).
			Find(&accounts)
	} else {
		result = tx.Set("gorm:query_option", "FOR UPDATE").
			Where("status = ? AND (processing_status IS NULL OR processing_status = 0)", 1).
			Order("id ASC, ISNULL(last_sync_list_time) DESC, last_sync_list_time ASC").
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

	// 【优化】使用批量更新而不是循环更新，减少锁持有时间
	now := time.Now()
	accountIDs := make([]int, len(accounts))
	for i, account := range accounts {
		accountIDs[i] = account.ID
	}

	// 批量更新所有选中的账号
	if err := tx.Model(&PrimeEmailAccount{}).
		Where("id IN (?)", accountIDs).
		Updates(map[string]interface{}{
			"last_sync_list_time": now,
			"processing_status":   1, // 标记为处理中
		}).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	log.Printf("[邮件同步] 成功批量更新 %d 个账号状态", len(accounts))
	return accounts, nil
}

// UpdateLastSyncListTimeOnComplete 在邮件列表同步完成后更新真正的同步时间
func UpdateLastSyncListTimeOnComplete(accountID int) error {
	now := time.Now()
	result := db.DB().Model(&PrimeEmailAccount{}).
		Where("id = ?", accountID).
		Updates(map[string]interface{}{
			"last_sync_list_time": now,
			"processing_status":   0, // 标记为空闲
		})
	return result.Error
}

// ResetSyncListTimeOnFailure 在邮件列表同步失败后，将同步时间重置为较早的时间，让它能够被重新选择
func ResetSyncListTimeOnFailure(accountID int) error {
	// 将失败的账号的同步时间设置为1小时前，这样它会在下次请求时被优先选择
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	result := db.DB().Model(&PrimeEmailAccount{}).
		Where("id = ?", accountID).
		Updates(map[string]interface{}{
			"last_sync_list_time": oneHourAgo,
			"processing_status":   0, // 标记为空闲
		})
	return result.Error
}

// CleanupStuckProcessingAccounts 清理卡死的处理状态账号
// timeoutMinutes: 超过多少分钟认为是卡死
// node: 指定节点，0表示所有节点
// 返回清理的账号数量和错误
func CleanupStuckProcessingAccounts(timeoutMinutes int, node int) (int, error) {
	// 计算超时时间点
	timeoutThreshold := time.Now().Add(-time.Duration(timeoutMinutes) * time.Minute)

	// 构建查询条件
	db := db.DB().Model(&PrimeEmailAccount{})

	// processing_status = 1 且最后更新时间超过阈值的账号
	whereCondition := "processing_status = 1 AND (last_sync_content_time < ? OR last_sync_list_time < ?)"
	args := []interface{}{timeoutThreshold, timeoutThreshold}

	if node > 0 {
		whereCondition += " AND node = ?"
		args = append(args, node)
	}

	// 找出符合条件的账号
	var stuckAccounts []PrimeEmailAccount
	if err := db.Where(whereCondition, args...).Find(&stuckAccounts).Error; err != nil {
		return 0, err
	}

	if len(stuckAccounts) == 0 {
		log.Printf("[状态清理] 没有发现卡死的账号")
		return 0, nil
	}

	// 记录要清理的账号
	var accountIDs []int
	for _, account := range stuckAccounts {
		accountIDs = append(accountIDs, account.ID)
		log.Printf("[状态清理] 发现卡死账号: ID=%d, Account=%s, Node=%d",
			account.ID, account.Account, account.Node)
	}

	// 批量重置状态
	updates := map[string]interface{}{
		"processing_status":      0,                              // 重置为空闲
		"last_sync_content_time": time.Now().Add(-2 * time.Hour), // 设置为2小时前，让其优先被选择
		"last_sync_list_time":    time.Now().Add(-2 * time.Hour), // 设置为2小时前，让其优先被选择
	}

	result := db.Where("id IN (?)", accountIDs).Updates(updates)
	if result.Error != nil {
		return 0, result.Error
	}

	cleanedCount := int(result.RowsAffected)
	log.Printf("[状态清理] 成功重置 %d 个卡死账号的状态", cleanedCount)

	return cleanedCount, nil
}
