package main

import (
	"flag"
	"fmt"
	"go_email/api"
	"go_email/config"
	"io"
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

	// 根据配置决定是否输出到控制台
	writers := viper.GetString("log.writers")
	if writers == "file,stdout" || writers == "stdout,file" {
		// 双重输出：文件和控制台
		multiWriter := io.MultiWriter(os.Stdout, f)
		stdlog.SetOutput(multiWriter)
		stdlog.SetFlags(stdlog.LstdFlags | stdlog.Lshortfile)
		stdlog.Printf("标准日志已配置为双重输出：控制台和文件 %s", logFile)
	} else {
		// 只输出到文件
		stdlog.SetOutput(f)
		stdlog.SetFlags(stdlog.LstdFlags | stdlog.Lshortfile)
		stdlog.Printf("标准日志已重定向到 %s", logFile)
	}
}

func main() {

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

	err := g.Run(viper.GetString("addr1"))
	if err != nil {
		panic(err)
	}
}
