package main

import (
	"fmt"
	"go_email/db"
	"go_email/model"
	"time"
)

func main() {
	// 插入单个邮件
	email := &model.PrimeEmail{
		EmailID:       12345,
		FromEmail:     "sender@example.com",
		Subject:       "测试邮件",
		Date:          "2023-06-01",
		HasAttachment: 1,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := email.Create(); err != nil {
		fmt.Printf("插入邮件失败: %v\n", err)
		return
	}
	fmt.Printf("邮件插入成功, ID: %d\n", email.ID)

	// 插入邮件内容
	content := &model.PrimeEmailContent{
		EmailID:     12345,
		Subject:     "测试邮件",
		FromEmail:   "sender@example.com",
		ToEmail:     "receiver@example.com",
		Date:        "2023-06-01",
		Content:     "这是邮件正文内容",
		HTMLContent: "<p>这是邮件正文内容</p>",
		Type:        1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := content.Create(); err != nil {
		fmt.Printf("插入邮件内容失败: %v\n", err)
		return
	}
	fmt.Printf("邮件内容插入成功, ID: %d\n", content.ID)

	// 插入邮件附件
	attachment := &model.PrimeEmailContentAttachment{
		FileName:  "test.pdf",
		SizeKb:    1024.5,
		MimeType:  "application/pdf",
		OssUrl:    "https://example.com/attachments/test.pdf",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := attachment.Create(); err != nil {
		fmt.Printf("插入附件失败: %v\n", err)
		return
	}
	fmt.Printf("附件插入成功, ID: %d\n", attachment.ID)

	// 演示使用事务同时插入多条记录
	insertWithTransaction()
}

// 使用事务同时插入邮件和邮件内容
func insertWithTransaction() {
	// 开始事务
	tx := db.DB().Begin()

	// 准备数据
	email := &model.PrimeEmail{
		EmailID:       54321,
		FromEmail:     "another@example.com",
		Subject:       "事务测试邮件",
		Date:          "2023-06-02",
		HasAttachment: 0,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	content := &model.PrimeEmailContent{
		EmailID:     54321,
		Subject:     "事务测试邮件",
		FromEmail:   "another@example.com",
		ToEmail:     "receiver@example.com",
		Date:        "2023-06-02",
		Content:     "这是事务测试的邮件正文",
		HTMLContent: "<p>这是事务测试的邮件正文</p>",
		Type:        1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// 在事务中插入邮件
	if err := tx.Create(email).Error; err != nil {
		tx.Rollback()
		fmt.Printf("事务中插入邮件失败: %v\n", err)
		return
	}

	// 在事务中插入邮件内容
	if err := tx.Create(content).Error; err != nil {
		tx.Rollback()
		fmt.Printf("事务中插入邮件内容失败: %v\n", err)
		return
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		fmt.Printf("提交事务失败: %v\n", err)
		return
	}

	fmt.Println("使用事务成功插入了邮件和邮件内容")
}
