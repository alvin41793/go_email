package crontab

import (
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"log"
)

func newWithSeconds() *cron.Cron {
	secondParser := cron.NewParser(cron.Second | cron.Minute |
		cron.Hour | cron.Dom | cron.Month | cron.DowOptional | cron.Descriptor)
	return cron.New(cron.WithParser(secondParser), cron.WithChain())
}

// 定时任务 只在一台服务器上执行
func Cron() {

	worker := newWithSeconds()
	//"*/1 * * * * *"
	_, err := worker.AddFunc("0 */2 * * * *", func() { //每3分钟
		//
		ListEmails(c * gin.Context)

	})
	if err != nil {
		log.Println(err)
	}

	worker.Start()

}
