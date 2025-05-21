package api

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SetupRouter 设置路由
func SetupRouter() *gin.Engine {
	r := gin.Default()

	// 配置CORS
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length", "Content-Disposition"},
		AllowCredentials: true,
	}))

	// API 路由组
	v1 := r.Group("/api/v1")
	{
		// 邮件相关路由
		emails := v1.Group("/emails")
		{
			// 获取邮件列表
			emails.GET("/list", ListEmails)

			// 获取邮件内容
			emails.GET("/content/:uid", GetEmailContent)

			// 获取附件列表
			emails.GET("/attachments/:uid", ListAttachments)

			// 发送邮件
			emails.POST("/send", SendEmail)
		}
	}

	return r
}
