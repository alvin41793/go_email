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
	maxGoroutines   int64
	currentCount    int64
	mutex           sync.RWMutex
	goroutines      map[string]*SafeGoroutineInfo
	cleanupInterval time.Duration
	defaultTimeout  time.Duration
	onPanic         func(goroutineID string, panicValue interface{})
	onComplete      func(goroutineID string, duration time.Duration)
}

// SafeGoroutineInfo 安全协程信息
type SafeGoroutineInfo struct {
	ID        string
	Name      string
	StartTime time.Time
	Context   context.Context
	Cancel    context.CancelFunc
	Timeout   time.Duration
}

// GoroutineStats 协程统计信息
type GoroutineStats struct {
	ManagedGoroutines     int64             `json:"managed_goroutines"`
	MaxGoroutines         int64             `json:"max_goroutines"`
	SystemGoroutines      int               `json:"system_goroutines"`
	ActiveGoroutines      int               `json:"active_goroutines"`
	CategoryStats         map[string]int    `json:"category_stats"`
	LongRunning           []LongRunningInfo `json:"long_running"`
	UnifiedSyncGoroutines int32             `json:"unified_sync_goroutines"`
}

// LongRunningInfo 长时间运行的协程信息
type LongRunningInfo struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Duration  time.Duration `json:"duration"`
	StartTime time.Time     `json:"start_time"`
}

// SafeGoroutineConfig 协程管理器配置
type SafeGoroutineConfig struct {
	MaxGoroutines   int64
	CleanupInterval time.Duration
	DefaultTimeout  time.Duration
	OnPanic         func(goroutineID string, panicValue interface{})
	OnComplete      func(goroutineID string, duration time.Duration)
}

// 全局安全协程管理器
var GlobalSafeGoroutineManager = NewSafeGoroutineManager(&SafeGoroutineConfig{
	MaxGoroutines:   150, // 增加最大协程数，支持更多邮件同步
	CleanupInterval: 2 * time.Minute,
	DefaultTimeout:  60 * time.Minute, // 增加到60分钟，确保邮件同步不会被过早清理
})

// NewSafeGoroutineManager 创建新的协程管理器
func NewSafeGoroutineManager(config *SafeGoroutineConfig) *SafeGoroutineManager {
	if config == nil {
		config = &SafeGoroutineConfig{
			MaxGoroutines:   50,
			CleanupInterval: 5 * time.Minute,
			DefaultTimeout:  30 * time.Minute,
		}
	}

	sgm := &SafeGoroutineManager{
		maxGoroutines:   config.MaxGoroutines,
		goroutines:      make(map[string]*SafeGoroutineInfo),
		cleanupInterval: config.CleanupInterval,
		defaultTimeout:  config.DefaultTimeout,
		onPanic:         config.OnPanic,
		onComplete:      config.OnComplete,
	}

	// 启动清理协程
	sgm.startCleanupRoutine()
	log.Printf("[协程管理] 协程管理器已初始化，最大协程数: %d", sgm.maxGoroutines)
	return sgm
}

// StartSafeGoroutine 启动一个安全的协程
func (sgm *SafeGoroutineManager) StartSafeGoroutine(ctx context.Context, name string, fn func(context.Context)) error {
	return sgm.StartSafeGoroutineWithTimeout(ctx, name, sgm.defaultTimeout, fn)
}

// StartSafeGoroutineWithTimeout 启动一个带超时的安全协程
func (sgm *SafeGoroutineManager) StartSafeGoroutineWithTimeout(ctx context.Context, name string, timeout time.Duration, fn func(context.Context)) error {
	// 检查是否超过最大协程数
	if atomic.LoadInt64(&sgm.currentCount) >= sgm.maxGoroutines {
		return fmt.Errorf("超过最大协程数限制: %d", sgm.maxGoroutines)
	}

	// 创建带超时的context
	var goroutineCtx context.Context
	var cancel context.CancelFunc

	if timeout > 0 {
		goroutineCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		goroutineCtx, cancel = context.WithCancel(ctx)
	}

	// 生成协程ID
	goroutineID := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())

	// 注册协程信息
	info := &SafeGoroutineInfo{
		ID:        goroutineID,
		Name:      name,
		StartTime: time.Now(),
		Context:   goroutineCtx,
		Cancel:    cancel,
		Timeout:   timeout,
	}

	sgm.mutex.Lock()
	sgm.goroutines[goroutineID] = info
	sgm.mutex.Unlock()

	// 增加计数
	atomic.AddInt64(&sgm.currentCount, 1)

	log.Printf("[协程管理] 启动协程: %s, 当前数量: %d/%d, 超时时间: %v", goroutineID,
		atomic.LoadInt64(&sgm.currentCount), sgm.maxGoroutines, timeout)

	// 启动协程
	go func() {
		startTime := time.Now()

		defer func() {
			duration := time.Since(startTime)

			// 恢复panic
			if r := recover(); r != nil {
				log.Printf("[协程管理] 协程 %s 发生panic: %v", goroutineID, r)
				if sgm.onPanic != nil {
					sgm.onPanic(goroutineID, r)
				}
			}

			// 清理协程
			sgm.cleanupGoroutine(goroutineID)

			// 调用完成回调
			if sgm.onComplete != nil {
				sgm.onComplete(goroutineID, duration)
			}

			log.Printf("[协程管理] 协程 %s 完成，运行时间: %v, 剩余数量: %d",
				goroutineID, duration, atomic.LoadInt64(&sgm.currentCount))
		}()

		// 执行实际的协程函数
		fn(goroutineCtx)
	}()

	return nil
}

// cleanupGoroutine 清理协程信息（不取消context）
func (sgm *SafeGoroutineManager) cleanupGoroutine(goroutineID string) {
	sgm.mutex.Lock()
	defer sgm.mutex.Unlock()

	if info, exists := sgm.goroutines[goroutineID]; exists {
		// 不要在正常完成时调用Cancel()，因为可能有子协程还在运行
		// 只有在强制清理时才需要取消context
		// info.Cancel() // 移除这行，避免过早取消context

		// 删除记录
		delete(sgm.goroutines, goroutineID)
		// 减少计数
		atomic.AddInt64(&sgm.currentCount, -1)

		log.Printf("[安全协程] 协程 %s (%s) 已清理，剩余: %d",
			goroutineID, info.Name, atomic.LoadInt64(&sgm.currentCount))
	}
}

// cleanupGoroutineWithCancel 强制清理协程信息（会取消context）
func (sgm *SafeGoroutineManager) cleanupGoroutineWithCancel(goroutineID string) {
	sgm.mutex.Lock()
	defer sgm.mutex.Unlock()

	if info, exists := sgm.goroutines[goroutineID]; exists {
		// 强制取消context
		info.Cancel()
		// 删除记录
		delete(sgm.goroutines, goroutineID)
		// 减少计数
		atomic.AddInt64(&sgm.currentCount, -1)

		log.Printf("[安全协程] 协程 %s (%s) 已强制清理，剩余: %d",
			goroutineID, info.Name, atomic.LoadInt64(&sgm.currentCount))
	}
}

// GetGoroutineStats 获取协程统计
func (sgm *SafeGoroutineManager) GetGoroutineStats() GoroutineStats {
	sgm.mutex.RLock()
	defer sgm.mutex.RUnlock()

	stats := GoroutineStats{
		ManagedGoroutines: atomic.LoadInt64(&sgm.currentCount),
		MaxGoroutines:     sgm.maxGoroutines,
		SystemGoroutines:  runtime.NumGoroutine(),
		ActiveGoroutines:  len(sgm.goroutines),
		CategoryStats:     make(map[string]int),
		LongRunning:       make([]LongRunningInfo, 0),
	}

	now := time.Now()

	// 添加各类协程统计
	for _, info := range sgm.goroutines {
		stats.CategoryStats[info.Name]++

		// 检查长时间运行的协程（超过10分钟）
		duration := now.Sub(info.StartTime)
		if duration > 10*time.Minute {
			stats.LongRunning = append(stats.LongRunning, LongRunningInfo{
				ID:        info.ID,
				Name:      info.Name,
				Duration:  duration,
				StartTime: info.StartTime,
			})
		}
	}

	return stats
}

// CleanupTimeoutGoroutines 清理超时协程
func (sgm *SafeGoroutineManager) CleanupTimeoutGoroutines(timeout time.Duration) int {
	sgm.mutex.Lock()
	defer sgm.mutex.Unlock()

	now := time.Now()
	var toCleanup []string

	for id, info := range sgm.goroutines {
		// 使用协程的实际超时时间，而不是全局默认超时时间
		actualTimeout := info.Timeout
		if actualTimeout <= 0 {
			actualTimeout = timeout // 如果没有设置超时，使用传入的timeout
		}

		// 给协程额外的缓冲时间（10分钟），避免过早清理
		effectiveTimeout := actualTimeout + 10*time.Minute

		if now.Sub(info.StartTime) > effectiveTimeout {
			toCleanup = append(toCleanup, id)
		}
	}

	for _, id := range toCleanup {
		if info, exists := sgm.goroutines[id]; exists {
			log.Printf("[协程管理] 清理超时协程: %s (%s), 运行时间: %v, 设定超时: %v",
				id, info.Name, now.Sub(info.StartTime), info.Timeout)

			// 强制取消超时协程的context
			info.Cancel()
			// 删除记录
			delete(sgm.goroutines, id)
			// 减少计数
			atomic.AddInt64(&sgm.currentCount, -1)
		}
	}

	return len(toCleanup)
}

// CancelGoroutine 取消指定协程
func (sgm *SafeGoroutineManager) CancelGoroutine(goroutineID string) bool {
	sgm.mutex.Lock()
	defer sgm.mutex.Unlock()

	if info, exists := sgm.goroutines[goroutineID]; exists {
		// 取消context
		info.Cancel()
		// 删除记录
		delete(sgm.goroutines, goroutineID)
		// 减少计数
		atomic.AddInt64(&sgm.currentCount, -1)

		log.Printf("[协程管理] 手动取消协程: %s", goroutineID)
		return true
	}
	return false
}

// CancelAllGoroutines 取消所有协程
func (sgm *SafeGoroutineManager) CancelAllGoroutines() int {
	sgm.mutex.RLock()
	var goroutineIDs []string
	for id := range sgm.goroutines {
		goroutineIDs = append(goroutineIDs, id)
	}
	sgm.mutex.RUnlock()

	canceledCount := 0
	for _, id := range goroutineIDs {
		if sgm.CancelGoroutine(id) {
			canceledCount++
		}
	}

	log.Printf("[协程管理] 取消了 %d 个协程", canceledCount)
	return canceledCount
}

// startCleanupRoutine 启动清理协程
func (sgm *SafeGoroutineManager) startCleanupRoutine() {
	go func() {
		ticker := time.NewTicker(sgm.cleanupInterval)
		defer ticker.Stop()

		for range ticker.C {
			// 清理超时协程（默认超时时间的2倍）
			cleanupTimeout := sgm.defaultTimeout * 2
			cleanedCount := sgm.CleanupTimeoutGoroutines(cleanupTimeout)

			// 获取统计信息
			stats := sgm.GetGoroutineStats()

			// 记录日志
			if cleanedCount > 0 {
				log.Printf("[协程管理] 清理了 %d 个超时协程", cleanedCount)
			}

			// 检查系统协程数
			if stats.SystemGoroutines > 200 {
				log.Printf("[协程管理] 警告: 系统协程数过多 %d", stats.SystemGoroutines)
			}

			// 检查长时间运行的协程
			if len(stats.LongRunning) > 0 {
				log.Printf("[协程管理] 警告: 发现 %d 个长时间运行的协程", len(stats.LongRunning))
				for _, lr := range stats.LongRunning {
					log.Printf("[协程管理] 长时间运行: %s (%s) - %v", lr.ID, lr.Name, lr.Duration)
				}
			}
		}
	}()

	log.Printf("[协程管理] 清理协程已启动，间隔: %v", sgm.cleanupInterval)
}

// SetMaxGoroutines 设置最大协程数
func (sgm *SafeGoroutineManager) SetMaxGoroutines(max int64) {
	atomic.StoreInt64(&sgm.maxGoroutines, max)
	log.Printf("[协程管理] 更新最大协程数: %d", max)
}

// GetCurrentCount 获取当前协程数
func (sgm *SafeGoroutineManager) GetCurrentCount() int64 {
	return atomic.LoadInt64(&sgm.currentCount)
}

// IsAtCapacity 检查是否已达到容量上限
func (sgm *SafeGoroutineManager) IsAtCapacity() bool {
	return atomic.LoadInt64(&sgm.currentCount) >= sgm.maxGoroutines
}
