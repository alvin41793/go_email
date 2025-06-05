package model

import (
	"go_email/db"
	"go_email/pkg/utils"
	"log"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

// PrimeEmailContent 邮件内容表结构
type PrimeEmailContent struct {
	ID            uint           `gorm:"primarykey;column:id" json:"id"`
	EmailID       int            `gorm:"column:email_id" json:"email_id"`
	Subject       string         `gorm:"column:subject;size:255" json:"subject"`                // 主题
	FromEmail     string         `gorm:"column:from_email;size:255" json:"from_email"`          // 发送者
	ToEmail       string         `gorm:"column:to_email;size:255" json:"to_email"`              // 接收者
	Date          string         `gorm:"column:date;size:255" json:"date"`                      // 邮件日期
	Content       string         `gorm:"column:content;type:text" json:"content"`               // 正文
	HTMLContent   string         `gorm:"column:html_content;type:longtext" json:"html_content"` // html正文
	HasAttachment int            `gorm:"column:has_attachment;" json:"has_attachment"`          // 附件 0:没有1:有
	Type          int            `gorm:"column:type" json:"type"`                               // 邮件类型
	Status        int            `gorm:"column:status" json:"status"`
	CreatedAt     utils.JsonTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     utils.JsonTime `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建一条邮件内容记录
func (e *PrimeEmailContent) Create() error {
	return db.DB().Create(e).Error
}

// GetContentByEmailID 根据EmailID获取邮件内容
func GetContentByEmailID(emailID int) (*PrimeEmailContent, error) {
	var content PrimeEmailContent
	err := db.DB().Where("email_id = ?", emailID).First(&content).Error
	return &content, err
}

// 清理非法UTF-8字符
func sanitizeUTF8(input string) string {
	if utf8.ValidString(input) {
		return input
	}

	log.Printf("[字符集处理] 检测到非法UTF-8字符，进行清洗")

	// 将非法UTF-8字符替换为空格
	result := strings.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return ' '
		}
		return r
	}, input)

	// 如果仍然有非法字符，使用更激进的方式：只保留ASCII字符
	if !utf8.ValidString(result) {
		log.Printf("[字符集处理] 第一次清洗后仍有非法字符，只保留ASCII字符")
		result = strings.Map(func(r rune) rune {
			if r <= 127 {
				return r
			}
			return ' '
		}, input)
	}

	return result
}

// CreateWithTransaction 使用事务创建邮件内容
func (e *PrimeEmailContent) CreateWithTransaction(tx *gorm.DB) error {
	log.Printf("[邮件内容保存] 准备保存邮件内容: ID=%d, 主题=%s, 发件人=%s", e.EmailID, e.Subject, e.FromEmail)

	// 清理所有文本字段，确保它们是有效的UTF-8字符串
	e.Subject = utils.SanitizeUTF8(e.Subject)
	e.FromEmail = utils.SanitizeUTF8(e.FromEmail)
	e.ToEmail = utils.SanitizeUTF8(e.ToEmail)
	e.Date = utils.SanitizeUTF8(e.Date)
	e.Content = utils.SanitizeUTF8(e.Content)
	e.HTMLContent = utils.SanitizeUTF8(e.HTMLContent)
	e.HasAttachment = e.HasAttachment
	e.Status = -1
	err := tx.Create(e).Error
	if err != nil {
		log.Printf("[邮件内容保存] 保存邮件内容失败: ID=%d, 错误=%v", e.EmailID, err)
		return err
	}

	log.Printf("[邮件内容保存] 成功保存邮件内容: ID=%d", e.EmailID)
	return nil
}

// PrimeEmailForwardMetrics 邮件转发耗时统计结构体
type PrimeEmailForwardMetrics struct {
	ID                 uint           `gorm:"primarykey;column:id" json:"id"`
	EmailID            int            `gorm:"column:email_id" json:"email_id"`                       // 邮件ID
	TotalDuration      int64          `gorm:"column:total_duration" json:"total_duration"`           // 总耗时(毫秒)
	FetchDuration      int64          `gorm:"column:fetch_duration" json:"fetch_duration"`           // 获取邮件耗时(毫秒)
	BuildDuration      int64          `gorm:"column:build_duration" json:"build_duration"`           // 构建邮件耗时(毫秒)
	AttachmentDuration int64          `gorm:"column:attachment_duration" json:"attachment_duration"` // 处理附件耗时(毫秒)
	SendDuration       int64          `gorm:"column:send_duration" json:"send_duration"`             // 发送邮件耗时(毫秒)
	AttachmentCount    int            `gorm:"column:attachment_count" json:"attachment_count"`       // 附件数量
	EmailSize          int64          `gorm:"column:email_size" json:"email_size"`                   // 邮件大小(字节)
	Status             int            `gorm:"column:status" json:"status"`                           // 状态: 1成功, -1失败
	ErrorMessage       string         `gorm:"column:error_message;size:255" json:"error_message"`    // 错误信息
	CreatedAt          utils.JsonTime `gorm:"column:created_at" json:"created_at"`                   // 创建时间
}

// CreateForwardMetrics 创建一条邮件转发耗时记录
func CreateForwardMetrics(metrics *PrimeEmailForwardMetrics) error {
	return db.DB().Create(metrics).Error
}

// GetForwardMetricsByEmailID 根据EmailID获取邮件转发耗时记录
func GetForwardMetricsByEmailID(emailID int) (*PrimeEmailForwardMetrics, error) {
	var metrics PrimeEmailForwardMetrics
	err := db.DB().Where("email_id = ?", emailID).First(&metrics).Error
	return &metrics, err
}

// GetForwardMetricsStats 获取邮件转发耗时统计
func GetForwardMetricsStats() (map[string]interface{}, error) {
	var stats struct {
		AvgTotal      float64 `json:"avg_total"`
		AvgFetch      float64 `json:"avg_fetch"`
		AvgBuild      float64 `json:"avg_build"`
		AvgAttachment float64 `json:"avg_attachment"`
		AvgSend       float64 `json:"avg_send"`
		MaxTotal      int64   `json:"max_total"`
		MinTotal      int64   `json:"min_total"`
		TotalCount    int64   `json:"total_count"`
		SuccessCount  int64   `json:"success_count"`
		FailCount     int64   `json:"fail_count"`
	}

	// 计算平均值
	query := `
		SELECT 
			AVG(total_duration) as avg_total,
			AVG(fetch_duration) as avg_fetch,
			AVG(build_duration) as avg_build,
			AVG(attachment_duration) as avg_attachment,
			AVG(send_duration) as avg_send,
			MAX(total_duration) as max_total,
			MIN(total_duration) as min_total,
			COUNT(*) as total_count,
			SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END) as success_count,
			SUM(CASE WHEN status = -1 THEN 1 ELSE 0 END) as fail_count
		FROM prime_email_forward_metrics
	`

	err := db.DB().Raw(query).Scan(&stats).Error
	if err != nil {
		return nil, err
	}

	// 转换为map返回
	result := map[string]interface{}{
		"平均总耗时(毫秒)":    stats.AvgTotal,
		"平均获取耗时(毫秒)":   stats.AvgFetch,
		"平均构建耗时(毫秒)":   stats.AvgBuild,
		"平均附件处理耗时(毫秒)": stats.AvgAttachment,
		"平均发送耗时(毫秒)":   stats.AvgSend,
		"最大总耗时(毫秒)":    stats.MaxTotal,
		"最小总耗时(毫秒)":    stats.MinTotal,
		"总记录数":         stats.TotalCount,
		"成功数":          stats.SuccessCount,
		"失败数":          stats.FailCount,
		"成功率":          float64(stats.SuccessCount) / float64(stats.TotalCount) * 100,
	}

	return result, nil
}
