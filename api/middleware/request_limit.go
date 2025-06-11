package middleware

import (
	"go_email/pkg/utils"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// 请求限制相关的全局变量
type requestLimiter struct {
	currentRequests int32
	maxRequests     int32
	mutex           sync.Mutex
	// 添加请求跟踪，用于超时清理
	activeRequests map[string]*requestInfo
}

type requestInfo struct {
	startTime time.Time
	done      chan struct{}
}

var limiters = make(map[string]*requestLimiter)
var limitersLock sync.RWMutex

// 启动清理goroutine
func init() {
	go cleanupTimeoutRequests()
}

// 清理超时请求的goroutine
func cleanupTimeoutRequests() {
	ticker := time.NewTicker(30 * time.Second) // 每30秒检查一次
	defer ticker.Stop()

	for range ticker.C {
		limitersLock.RLock()
		for path, limiter := range limiters {
			limiter.mutex.Lock()
			now := time.Now()
			timeoutDuration := 5 * time.Minute // 5分钟超时

			for reqId, reqInfo := range limiter.activeRequests {
				if now.Sub(reqInfo.startTime) > timeoutDuration {
					select {
					case <-reqInfo.done:
						// 请求已经完成，从map中删除
						delete(limiter.activeRequests, reqId)
					default:
						// 请求超时，强制清理
						log.Printf("[请求限制] 清理超时请求: %s, 路径: %s, 超时时长: %v",
							reqId, path, now.Sub(reqInfo.startTime))
						close(reqInfo.done)
						delete(limiter.activeRequests, reqId)
						atomic.AddInt32(&limiter.currentRequests, -1)
					}
				}
			}
			limiter.mutex.Unlock()
		}
		limitersLock.RUnlock()
	}
}

// RequestLimit 创建一个请求限制中间件
// maxRequests: 最大并发请求数
func RequestLimit(maxRequests int32) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 使用路由路径作为限制器的key
		path := c.FullPath()

		// 获取或创建限制器
		limitersLock.RLock()
		limiter, exists := limiters[path]
		limitersLock.RUnlock()

		if !exists {
			limitersLock.Lock()
			// 双重检查
			if limiter, exists = limiters[path]; !exists {
				limiter = &requestLimiter{
					currentRequests: 0,
					maxRequests:     maxRequests,
					activeRequests:  make(map[string]*requestInfo),
				}
				limiters[path] = limiter
			}
			limitersLock.Unlock()
		}

		// 检查是否超过限制
		current := atomic.LoadInt32(&limiter.currentRequests)
		if current >= limiter.maxRequests {
			utils.SendResponse(c, nil, "请求数量超过限制，请稍后再试")
			c.Abort()
			return
		}

		// 生成请求ID（使用时间戳+goroutine信息）
		reqId := time.Now().Format("20060102150405.000000")

		// 增加当前请求计数
		atomic.AddInt32(&limiter.currentRequests, 1)

		// 注册请求信息
		reqInfo := &requestInfo{
			startTime: time.Now(),
			done:      make(chan struct{}),
		}

		limiter.mutex.Lock()
		limiter.activeRequests[reqId] = reqInfo
		limiter.mutex.Unlock()

		// 监听context取消和超时
		go func() {
			select {
			case <-c.Request.Context().Done():
				// 请求被取消或超时
				limiter.mutex.Lock()
				if _, exists := limiter.activeRequests[reqId]; exists {
					log.Printf("[请求限制] 请求被取消或超时: %s, 路径: %s", reqId, path)
					close(reqInfo.done)
					delete(limiter.activeRequests, reqId)
					atomic.AddInt32(&limiter.currentRequests, -1)
				}
				limiter.mutex.Unlock()
			case <-reqInfo.done:
				// 请求正常完成
				return
			}
		}()

		// 请求完成后清理资源
		defer func() {
			limiter.mutex.Lock()
			defer limiter.mutex.Unlock()

			if _, exists := limiter.activeRequests[reqId]; exists {
				close(reqInfo.done)
				delete(limiter.activeRequests, reqId)
				atomic.AddInt32(&limiter.currentRequests, -1)
			}
		}()

		c.Next()
	}
}
