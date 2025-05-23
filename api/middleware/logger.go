package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zxmrlc/log"
)

// Logger is a middleware function that logs the each request.
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		start := time.Now()

		// 处理请求
		c.Next()

		// 结束时间
		end := time.Now()
		// 执行时间
		latency := end.Sub(start)

		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		path := c.Request.URL.Path
		userAgent := c.Request.UserAgent()

		// 使用log包记录请求信息
		log.Infof("| %3d | %13v | %15s | %s | %s | %s |",
			statusCode,
			latency,
			clientIP,
			method,
			path,
			userAgent,
		)

		// 同时在控制台打印日志
		fmt.Printf("| %3d | %13v | %15s | %s | %s | %s |\n",
			statusCode,
			latency,
			clientIP,
			method,
			path,
			userAgent,
		)
	}
}
