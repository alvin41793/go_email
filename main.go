package main

import (
	"flag"
	"fmt"
	"go_email/api"
	"go_email/config"
	"go_email/pkg/mailclient"
	stdlog "log"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func initStdLog() {
	// 获取默认日志文件路径
	logFile := viper.GetString("log.logger_file")

	// 确保日志路径是子目录
	if logFile == "" || logFile == "log/api_server.log" {
		logFile = "log/api_server.log"
	}

	// 确保目录存在
	dir := filepath.Dir(logFile)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0777)
		if err != nil {
			fmt.Println("无法创建日志目录:", err)
		}
	}

	// 打开日志文件
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		fmt.Printf("打开标准日志文件失败，继续使用标准输出: %v\n", err)
		return
	}

	// 设置标准日志输出到文件
	stdlog.SetOutput(f)
	stdlog.SetFlags(stdlog.LstdFlags | stdlog.Lshortfile)
	stdlog.Printf("标准日志已重定向到 %s", logFile)
}

func main() {
	// 获取邮箱配置
	emailConfig, err := mailclient.GetEmailConfig()
	if err != nil {
		stdlog.Fatalf("无法加载邮箱配置: %v", err)
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

	env := flag.String("env", "", "环境名称（如 debug, prod）")
	flag.Parse()

	// 根据环境名称读取配置文件
	if *env == "" {
		stdlog.Fatal("必须指定环境参数 -env")
	}

	if err := config.Init(*env); err != nil {
		panic(err)
	}

	// 初始化标准库日志，确保在设置gin之前初始化
	initStdLog()

	// Set gin mode.
	gin.SetMode(viper.GetString("run_mode"))

	// 设置路由
	g := gin.New()
	api.Load1(
		g,
	)

	// 连接数据库

	err = g.Run(viper.GetString("addr1"))
	if err != nil {
		panic(err)
	}
}
