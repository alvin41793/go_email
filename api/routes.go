package api

import (
	"go_email/api/middleware"
	"net/http"

	"github.com/gin-gonic/gin"
)

func Load1(g *gin.Engine, mw ...gin.HandlerFunc) *gin.Engine {
	g.Use(gin.Recovery())
	// 使用Gin自带的Logger中间件
	//g.Use(gin.Logger())
	g.Use(middleware.Logger())
	//g.Use(middleware.Auth())
	g.Use(middleware.NoCache)
	g.Use(middleware.Options)
	g.Use(middleware.Secure)
	g.Use(middleware.Auth()) // 启用Auth中间件进行token验证
	g.Use(mw...)
	//定时任务
	//crontab.Cron()
	g.NoRoute(func(c *gin.Context) {
		c.String(http.StatusNotFound, "The incorrect API route...")
	})
	// API 路由组
	v1 := g.Group("/api/v1")
	{
		// 系统状态相关路由
		system := v1.Group("/system")
		{
			// 获取协程统计信息
			system.GET("/goroutine-stats", GetGoroutineStats)
			// 获取详细的协程统计信息
			system.GET("/goroutine-stats/detailed", GetDetailedGoroutineStats)
			// 协程监控端点，用于健康检查
			system.GET("/goroutine-monitor", MonitorGoroutines)
			// 强制清理协程
			system.POST("/goroutines/cleanup", ForceCleanupGoroutines)
			// 清理卡死账号状态
			system.POST("/cleanup-stuck-accounts", CleanupStuckAccounts)
		}

		// 邮件相关路由
		emails := v1.Group("/emails")
		{
			// 统一同步接口 - 合并邮件列表和内容同步
			emails.POST("/list", UnifiedEmailSync)

			// 通过指定uid获取邮件列表
			emails.POST("/list_by_uid", ListEmailsByUid)

			//转发邮件 - 限制最多10个并发请求
			//emails.POST("/tr_send", middleware.RequestLimit(10), GetForwardOriginalEmail)
			// 发送邮件
			//emails.POST("/send", SendEmail)
		}
	}

	return g
}
