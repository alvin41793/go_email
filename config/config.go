package config

import (
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"github.com/zxmrlc/log"
	"strings"
)

// Config 应用配置结构体
type EmailConfig struct {
	Email struct {
		IMAPServer   string `yaml:"imap_server"`
		SMTPServer   string `yaml:"smtp_server"`
		EmailAddress string `yaml:"email_address"`
		Password     string `yaml:"password"`
		IMAPPort     int    `yaml:"imap_port"`
		SMTPPort     int    `yaml:"smtp_port"`
		UseSSL       bool   `yaml:"use_ssl"`
	} `yaml:"email"`

	Server struct {
		Port int    `yaml:"port"`
		Host string `yaml:"host"`
	} `yaml:"server"`
}

// EmailConfig 邮箱配置
type EmailConfigInfo struct {
	IMAPServer   string
	SMTPServer   string
	EmailAddress string
	Password     string
	IMAPPort     int
	SMTPPort     int
	UseSSL       bool
}

type Config struct {
	Name string
}

func Init(cfg string) error {
	c := Config{
		Name: cfg,
	}

	// 初始化配置文件
	if err := c.initConfig(); err != nil {
		return err
	}

	// 不再初始化日志包，由各服务自行初始化

	// 监控配置文件变化并热加载程序
	c.watchConfig()

	return nil
}

func (c *Config) initConfig() error {
	if c.Name != "" {
		viper.SetConfigName(c.Name) // 如果指定了配置文件，则解析指定的配置文件
	}
	//println("文件名", c.Name)
	viper.AddConfigPath("./config")
	viper.SetConfigType("yaml") // 设置配置文件格式为YAML
	viper.AutomaticEnv()        // 读取匹配的环境变量
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)
	if err := viper.ReadInConfig(); err != nil { // viper解析配置文件
		return err
	}

	return nil
}

// GetEmailConfig 获取邮箱配置
func GetEmailConfig() (*EmailConfigInfo, error) {

	return &EmailConfigInfo{
		IMAPServer:   "imap.ipage.com",
		SMTPServer:   "smtp.ipage.com",
		EmailAddress: "aiteam@primeagencygroup.com",
		password: REDACTED,
		IMAPPort:     993,
		SMTPPort:     587,
		UseSSL:       true,
	}, nil
}

// 监控配置文件变化并热加载程序
func (c *Config) watchConfig() {
	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		log.Infof("Config file changed: %s", e.Name)
	})
}
