package utils

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// SafeGoroutineManager 安全协程管理器
type SafeGoroutineManager struct {
	maxGoroutines int64
	currentCount  int64
	mutex         sync.RWMutex
	goroutines    map[string]*SafeGoroutineInfo
}

// SafeGoroutineInfo 安全协程信息
type SafeGoroutineInfo struct {
	ID        string
	Name      string
	StartTime time.Time
	Context   context.Context
	Cancel    context.CancelFunc
}

// 全局安全协程管理器
var GlobalSafeGoroutineManager = &SafeGoroutineManager{
	maxGoroutines: 1000, // 最大1000个协程
	goroutines:    make(map[string]*SafeGoroutineInfo),
}

// StartSafeGoroutine 启动一个安全的协程
func (sgm *SafeGoroutineManager) StartSafeGoroutine(ctx context.Context, name string, fn func(context.Context)) error {
	// 检查是否超过最大协程数
	if atomic.LoadInt64(&sgm.currentCount) >= sgm.maxGoroutines {
		return fmt.Errorf("超过最大协程数限制: %d", sgm.maxGoroutines)
	}

	// 创建可取消的context
	goroutineCtx, cancel := context.WithCancel(ctx)

	// 生成协程ID
	goroutineID := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())

	// 注册协程信息
	sgm.mutex.Lock()
	sgm.goroutines[goroutineID] = &SafeGoroutineInfo{
		ID:        goroutineID,
		Name:      name,
		StartTime: time.Now(),
		Context:   goroutineCtx,
		Cancel:    cancel,
	}
	sgm.mutex.Unlock()

	// 增加计数
	atomic.AddInt64(&sgm.currentCount, 1)

	// 启动协程
	go func() {
		defer func() {
			// 协程完成时清理
			sgm.cleanupGoroutine(goroutineID)

			// 捕获panic
			if r := recover(); r != nil {
				log.Printf("[安全协程] 协程 %s 发生panic: %v", goroutineID, r)
			}
		}()

		// 执行实际的协程函数
		fn(goroutineCtx)
	}()

	return nil
}

// cleanupGoroutine 清理协程信息
func (sgm *SafeGoroutineManager) cleanupGoroutine(goroutineID string) {
	sgm.mutex.Lock()
	defer sgm.mutex.Unlock()

	if info, exists := sgm.goroutines[goroutineID]; exists {
		// 取消context
		info.Cancel()
		// 删除记录
		delete(sgm.goroutines, goroutineID)
		// 减少计数
		atomic.AddInt64(&sgm.currentCount, -1)

		log.Printf("[安全协程] 协程 %s (%s) 已清理，剩余: %d",
			goroutineID, info.Name, atomic.LoadInt64(&sgm.currentCount))
	}
}

// GetGoroutineStats 获取协程统计
func (sgm *SafeGoroutineManager) GetGoroutineStats() map[string]interface{} {
	sgm.mutex.RLock()
	defer sgm.mutex.RUnlock()

	stats := map[string]interface{}{
		"managed_goroutines": atomic.LoadInt64(&sgm.currentCount),
		"max_goroutines":     sgm.maxGoroutines,
		"system_goroutines":  runtime.NumGoroutine(),
		"active_goroutines":  len(sgm.goroutines),
	}

	// 添加各类协程统计
	categoryStats := make(map[string]int)
	for _, info := range sgm.goroutines {
		categoryStats[info.Name]++
	}
	stats["category_stats"] = categoryStats

	return stats
}

// CleanupTimeoutGoroutines 清理超时协程
func (sgm *SafeGoroutineManager) CleanupTimeoutGoroutines(timeout time.Duration) {
	sgm.mutex.Lock()
	defer sgm.mutex.Unlock()

	now := time.Now()
	var toCleanup []string

	for id, info := range sgm.goroutines {
		if now.Sub(info.StartTime) > timeout {
			toCleanup = append(toCleanup, id)
		}
	}

	for _, id := range toCleanup {
		if info, exists := sgm.goroutines[id]; exists {
			log.Printf("[安全协程] 清理超时协程: %s (%s), 运行时间: %v",
				id, info.Name, now.Sub(info.StartTime))

			// 取消context
			info.Cancel()
			// 删除记录
			delete(sgm.goroutines, id)
			// 减少计数
			atomic.AddInt64(&sgm.currentCount, -1)
		}
	}
}

// StartCleanupRoutine 启动清理协程
func (sgm *SafeGoroutineManager) StartCleanupRoutine() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute) // 每分钟清理一次
		defer ticker.Stop()

		for range ticker.C {
			// 清理超时协程（30分钟超时）
			sgm.CleanupTimeoutGoroutines(30 * time.Minute)

			// 检查系统协程数
			systemGoroutines := runtime.NumGoroutine()
			if systemGoroutines > 100 {
				log.Printf("[安全协程] 警告: 系统协程数过多 %d", systemGoroutines)
			}
		}
	}()
}

// 初始化协程管理器
func init() {
	// 启动清理协程
	GlobalSafeGoroutineManager.StartCleanupRoutine()
}
