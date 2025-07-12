package api

import (
	"context"
	"errors"
	"fmt"
	"go_email/db"
	"go_email/model"
	"go_email/pkg/mailclient"
	"go_email/pkg/utils"
	"log"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// syncAccountEmailList 同步单个账号的邮件列表
func syncAccountEmailList(mailClient *mailclient.MailClient, account model.PrimeEmailAccount, limit int, ctx context.Context) (int, error) {
	folder := "INBOX"

	// 使用数据库事务获取最新邮件ID并处理邮件
	tx := db.DB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("同步邮件列表时发生异常: %v", r)
		}
	}()

	lastEmail, err := model.GetLatestEmailWithTx(tx, account.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("账号ID %d 数据库中没有邮件记录，可能为第一次同步", account.ID)
		} else {
			tx.Rollback()
			return 0, fmt.Errorf("获取最大email_id失败: %v", err)
		}
	}

	var emailsResult []mailclient.EmailInfo
	if lastEmail.EmailID > 0 {
		log.Printf("账号ID %d 当前数据库最大email_id: %d", account.ID, lastEmail.EmailID)
		emailsResult, err = mailClient.ListEmailsFromUID(folder, limit, uint32(lastEmail.EmailID))
	} else {
		emailsResult, err = mailClient.ListEmails(folder, limit)
	}

	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("获取邮件列表失败: %v", err)
	}

	// 如果没有新邮件，也要更新同步时间后提交事务并返回
	if len(emailsResult) == 0 {
		if err := model.UpdateLastSyncTimeWithTx(tx, account.ID); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("更新最后同步时间失败: %v", err)
		}

		log.Printf("账号ID %d: 没有新邮件，但已更新最后同步时间", account.ID)

		if err := tx.Commit().Error; err != nil {
			return 0, fmt.Errorf("提交事务失败: %v", err)
		}
		return 0, nil
	}

	// 构建邮件列表
	var emailList []*model.PrimeEmail
	for _, email := range emailsResult {
		emailID, _ := strconv.Atoi(email.EmailID)
		emailInfo := &model.PrimeEmail{
			EmailID:       emailID,
			FromEmail:     utils.SanitizeUTF8(email.From),
			Subject:       utils.SanitizeUTF8(email.Subject),
			Date:          utils.SanitizeUTF8(email.Date),
			HasAttachment: 0,
			AccountId:     account.ID,
			Status:        -1, // 初始状态
			CreatedAt:     utils.JsonTime{Time: time.Now()},
		}

		if email.HasAttachments {
			emailInfo.HasAttachment = 1
		}

		emailList = append(emailList, emailInfo)
	}

	// 批量创建邮件记录（容错处理）
	result, err := model.BatchCreateEmailsWithStats(emailList, tx)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("批量创建邮件记录失败: %v", err)
	}

	// 更新账号的最后同步时间
	if err := model.UpdateLastSyncTimeWithTx(tx, account.ID); err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("更新最后同步时间失败: %v", err)
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return 0, fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("账号ID %d: 邮件列表同步成功 - 总计:%d, 成功:%d, 跳过:%d, 失败:%d",
		account.ID, result.TotalCount, result.SuccessCount, result.SkippedCount, result.FailedCount)

	return result.SuccessCount, nil
}

// syncAccountEmailContent 同步单个账号的邮件内容
func syncAccountEmailContent(account model.PrimeEmailAccount, limit int, ctx context.Context) (int, error) {
	// 获取该账号的待处理邮件
	accountEmails, err := model.GetEmailByStatusAndAccount(-1, account.ID, limit)
	if err != nil {
		return 0, fmt.Errorf("获取账号 %d 的邮件失败: %v", account.ID, err)
	}

	if len(accountEmails) == 0 {
		log.Printf("账号 %d (%s) - 没有需要处理的邮件", account.ID, account.Account)
		return 0, nil
	}

	log.Printf("账号 %d (%s) - 获取到 %d 封待处理邮件", account.ID, account.Account, len(accountEmails))

	folder := "INBOX"

	// 存储所有邮件内容和附件，以便后续批量存储
	allEmailData := make([]EmailContentData, 0, len(accountEmails))
	var successCount, failureCount int

	// 创建邮件客户端
	mailClient, err := newMailClient(account)
	if err != nil {
		return 0, fmt.Errorf("获取邮箱配置失败: %v", err)
	}

	for i, emailOne := range accountEmails {
		log.Printf("[邮件内容同步] 正在获取邮件内容，ID: %d", emailOne.EmailID)

		// 在处理每个邮件之间添加延迟，避免连接过于频繁
		if i > 0 {
			time.Sleep(time.Millisecond * 500)
		}

		// 检查context是否被取消
		select {
		case <-ctx.Done():
			log.Printf("[邮件内容同步] 上下文已取消，停止处理")
			return successCount, ctx.Err()
		default:
		}

		email, err := mailClient.GetEmailContent(uint32(emailOne.EmailID), folder)
		if err != nil {
			log.Printf("[邮件内容同步] 获取邮件内容失败，邮件ID: %d, 错误: %v", emailOne.EmailID, err)
			failureCount++

			// 设置邮件状态为失败
			resetErr := resetEmailStatus(emailOne.EmailID, -2)
			if resetErr != nil {
				log.Printf("[邮件内容同步] 设置邮件状态失败，邮件ID: %d, 错误: %v", emailOne.EmailID, resetErr)
			}
			continue
		}

		if email == nil {
			log.Printf("[邮件内容同步] 邮件内容为空，邮件ID: %d", emailOne.EmailID)
			failureCount++
			continue
		}

		// 创建邮件内容记录
		emailContent := &model.PrimeEmailContent{
			EmailID:       emailOne.EmailID,
			AccountId:     account.ID,
			Subject:       utils.SanitizeUTF8(email.Subject),
			FromEmail:     utils.SanitizeUTF8(email.From),
			ToEmail:       utils.SanitizeUTF8(email.To),
			Date:          utils.SanitizeUTF8(email.Date),
			Content:       utils.SanitizeUTF8(email.Body),
			HTMLContent:   utils.SanitizeUTF8(email.BodyHTML),
			HasAttachment: 0,
			Type:          0,
			Status:        -1,
			CreatedAt:     utils.JsonTime{Time: time.Now()},
		}

		// 处理附件
		var attachments []*model.PrimeEmailContentAttachment
		if email.Attachments != nil && len(email.Attachments) > 0 {
			emailContent.HasAttachment = 1
			for _, att := range email.Attachments {
				attachment := &model.PrimeEmailContentAttachment{
					EmailID:   emailOne.EmailID,
					AccountId: account.ID,
					FileName:  utils.SanitizeUTF8(att.Filename),
					SizeKb:    att.SizeKB, // 直接使用SizeKB字段
					MimeType:  utils.SanitizeUTF8(att.MimeType),
					OssUrl:    "", // 这里可以后续实现OSS上传
					CreatedAt: utils.JsonTime{Time: time.Now()},
				}
				attachments = append(attachments, attachment)
			}
		}

		// 添加到批量处理列表
		allEmailData = append(allEmailData, EmailContentData{
			EmailID:      emailOne.EmailID,
			AccountId:    account.ID,
			EmailContent: emailContent,
			Attachments:  attachments,
		})

		successCount++
		log.Printf("[邮件内容同步] 邮件 ID: %d 内容获取成功", emailOne.EmailID)
	}

	// 批量保存所有邮件内容和附件
	if len(allEmailData) > 0 {
		err := batchSaveEmailContents(allEmailData)
		if err != nil {
			log.Printf("[邮件内容同步] 批量保存邮件内容失败: %v", err)
			return 0, fmt.Errorf("批量保存邮件内容失败: %v", err)
		}

		log.Printf("[邮件内容同步] 账号 %d 批量保存完成: 成功 %d 封，失败 %d 封",
			account.ID, successCount, failureCount)
	}

	return successCount, nil
}

// resetEmailStatus 重置邮件状态
func resetEmailStatus(emailID int, status int) error {
	result := db.DB().Model(&model.PrimeEmail{}).Where("email_id = ?", emailID).Update("status", status)
	return result.Error
}

// EmailContentData 邮件内容数据结构
type EmailContentData struct {
	EmailID      int
	AccountId    int
	EmailContent *model.PrimeEmailContent
	Attachments  []*model.PrimeEmailContentAttachment
}

// batchSaveEmailContents 批量保存邮件内容和附件
func batchSaveEmailContents(emailDataList []EmailContentData) error {
	if len(emailDataList) == 0 {
		return nil
	}

	// 开始事务
	tx := db.DB().Begin()
	if tx.Error != nil {
		return tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("[批量保存邮件内容] 事务回滚: %v", r)
		}
	}()

	successCount := 0
	failedCount := 0

	for _, emailData := range emailDataList {
		// 保存邮件内容
		if err := emailData.EmailContent.CreateWithTransaction(tx); err != nil {
			log.Printf("[批量保存邮件内容] 保存邮件内容失败: EmailID=%d, 错误=%v", emailData.EmailID, err)
			failedCount++
			continue
		}

		// 保存附件
		for _, attachment := range emailData.Attachments {
			if err := attachment.CreateWithTransaction(tx); err != nil {
				log.Printf("[批量保存邮件内容] 保存附件失败: EmailID=%d, 文件名=%s, 错误=%v",
					emailData.EmailID, attachment.FileName, err)
				// 附件保存失败不影响邮件内容的保存
			}
		}

		// 更新邮件状态为已处理
		if err := tx.Model(&model.PrimeEmail{}).Where("email_id = ?", emailData.EmailID).Update("status", 1).Error; err != nil {
			log.Printf("[批量保存邮件内容] 更新邮件状态失败: EmailID=%d, 错误=%v", emailData.EmailID, err)
		}

		successCount++
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		log.Printf("[批量保存邮件内容] 提交事务失败: %v", err)
		return err
	}

	log.Printf("[批量保存邮件内容] 批量保存完成: 成功=%d, 失败=%d", successCount, failedCount)
	return nil
}
