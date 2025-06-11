package api

import (
	"go_email/api/middleware"
	crontab "go_email/cron"
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
	crontab.Cron()
	g.NoRoute(func(c *gin.Context) {
		c.String(http.StatusNotFound, "The incorrect API route...")
	})
	// API 路由组
	v1 := g.Group("/api/v1")
	{
		// 邮件相关路由
		emails := v1.Group("/emails")
		{
			// 同步多账号邮件
			emails.POST("/list", SyncMultipleAccounts)
			// 通过指定uid获取邮件列表
			emails.POST("/list_by_uid", ListEmailsByUid)

			// 获取邮件内容
			emails.POST("/content", GetEmailContentList)

			// 获取附件列表
			//emails.GET("/attachments/:uid", ListAttachments)

			//转发邮件 - 限制最多10个并发请求
			emails.POST("/tr_send", middleware.RequestLimit(10), GetForwardOriginalEmail)
			// 发送邮件
			//emails.POST("/send", SendEmail)
		}
	}

	return g
}
