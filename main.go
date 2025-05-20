package main

import (
	"fmt"
	"log"

	"go_email/api"
	"go_email/config"
)

func main() {
	// 获取邮箱配置
	emailConfig, err := config.GetEmailConfig()
	if err != nil {
		log.Fatalf("无法加载邮箱配置: %v", err)
	}

	// 初始化邮件客户端
	api.InitMailClient(
		emailConfig.IMAPServer,
		emailConfig.SMTPServer,
		emailConfig.EmailAddress,
		emailConfig.Password,
		emailConfig.IMAPPort,
		emailConfig.SMTPPort,
		emailConfig.UseSSL,
	)

	// 设置路由
	r := api.SetupRouter()

	// 获取服务端口
	port := config.GetServerPort()

	// 启动服务
	serverAddr := fmt.Sprintf(":%d", port)
	log.Printf("邮件服务启动在端口 %d", port)
	if err := r.Run(serverAddr); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
