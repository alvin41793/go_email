package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config 应用配置结构体
type Config struct {
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
type EmailConfig struct {
	IMAPServer   string
	SMTPServer   string
	EmailAddress string
	Password     string
	IMAPPort     int
	SMTPPort     int
	UseSSL       bool
}

// 获取配置文件路径
func getConfigPath() (string, error) {
	// 首先尝试当前工作目录
	configPath := "config/config.yaml"
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	// 然后尝试相对于可执行文件的路径
	execDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		return "", err
	}
	configPath = filepath.Join(execDir, "config/config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	// 再尝试用户主目录
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configPath = filepath.Join(homeDir, ".go_email/config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	return "", fmt.Errorf("配置文件未找到")
}

// LoadConfig 加载配置
func LoadConfig() (*Config, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return &config, nil
}

// GetEmailConfig 获取邮箱配置
func GetEmailConfig() (*EmailConfig, error) {
	config, err := LoadConfig()
	if err != nil {
		return nil, err
	}

	return &EmailConfig{
		IMAPServer:   config.Email.IMAPServer,
		SMTPServer:   config.Email.SMTPServer,
		EmailAddress: config.Email.EmailAddress,
		password: REDACTED
		IMAPPort:     config.Email.IMAPPort,
		SMTPPort:     config.Email.SMTPPort,
		UseSSL:       config.Email.UseSSL,
	}, nil
}

// GetServerPort 获取服务器端口
func GetServerPort() int {
	config, err := LoadConfig()
	if err != nil {
		return 8080 // 默认端口
	}
	return config.Server.Port
}
