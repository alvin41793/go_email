package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"go_email/db"
	"go_email/model"
	"go_email/pkg/mailclient"
	"go_email/pkg/utils"
	"io"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nwaples/rardecode/v2"
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
func syncAccountEmailContent(mailClient *mailclient.MailClient, account model.PrimeEmailAccount, limit int, ctx context.Context) (int, error) {
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
	startTime := time.Now()

	// 从context获取deadline，计算实际可用时间
	deadline, hasDeadline := ctx.Deadline()
	var safeTimeLimit time.Time
	if hasDeadline {
		// 提前2分钟结束，避免真正超时
		safeTimeLimit = deadline.Add(-2 * time.Minute)
		log.Printf("账号 %d - 启用智能超时保护，超时时限: %v，安全时限: %v", account.ID, deadline, safeTimeLimit)
	}

	// 存储所有邮件内容和附件，以便后续批量存储
	allEmailData := make([]EmailContentData, 0, len(accountEmails))
	var successCount, failureCount int

	// 性能监控变量
	var totalFetchTime, totalOSSTime time.Duration
	var attachmentCount int

	for i, emailOne := range accountEmails {
		currentTime := time.Now()
		elapsed := currentTime.Sub(startTime)

		log.Printf("[邮件内容同步] 正在获取邮件内容，ID: %d，进度: %d/%d，已耗时: %v",
			emailOne.EmailID, i+1, len(accountEmails), elapsed)

		// 在处理每个邮件之间添加延迟，避免连接过于频繁
		if i > 0 {
			time.Sleep(time.Millisecond * 500)
		}

		// 智能超时检测
		shouldStop := false
		select {
		case <-ctx.Done():
			shouldStop = true
			log.Printf("[邮件内容同步] 上下文已取消，立即停止处理")
		default:
			// 检查是否接近安全时限
			if hasDeadline && currentTime.After(safeTimeLimit) {
				shouldStop = true
				log.Printf("[邮件内容同步] 已接近安全时限，提前停止处理，当前时间: %v，安全时限: %v",
					currentTime, safeTimeLimit)
			}
		}

		if shouldStop {
			remainingEmails := len(accountEmails) - i
			log.Printf("[邮件内容同步] 停止处理，已处理: %d/%d，未处理: %d，总耗时: %v，平均每邮件: %v",
				i, len(accountEmails), remainingEmails, elapsed,
				time.Duration(int64(elapsed)/int64(max(i, 1))))

			// 如果有已处理的邮件，先保存它们（这些邮件的status会变成1）
			if len(allEmailData) > 0 {
				log.Printf("[邮件内容同步] 尝试保存已处理的 %d 封邮件（status: 0 → 1）", len(allEmailData))
				if saveErr := batchSaveEmailContents(allEmailData); saveErr != nil {
					log.Printf("[邮件内容同步] 保存已处理邮件失败: %v", saveErr)
				} else {
					log.Printf("[邮件内容同步] 成功保存已处理的 %d 封邮件（status已更新为1）", len(allEmailData))
					successCount = len(allEmailData)
				}
			}

			// 重置未处理邮件的状态：从0（处理中）回到-1（待处理），下次同步时会被重新处理
			if remainingEmails > 0 {
				log.Printf("[邮件内容同步] 开始重置 %d 封未处理邮件的状态（status: 0 → -1）", remainingEmails)
				var resetEmailIDs []int
				for j := i; j < len(accountEmails); j++ {
					resetEmailIDs = append(resetEmailIDs, accountEmails[j].EmailID)
				}

				// 批量重置状态
				if len(resetEmailIDs) > 0 {
					resetCount := 0
					for _, emailID := range resetEmailIDs {
						if resetErr := resetEmailStatus(emailID, -1); resetErr != nil {
							log.Printf("[邮件内容同步] 重置邮件状态失败，邮件ID: %d, 错误: %v", emailID, resetErr)
						} else {
							resetCount++
						}
					}
					log.Printf("[邮件内容同步] 成功重置 %d/%d 封邮件状态为-1，等待下次同步", resetCount, len(resetEmailIDs))
				}
			}

			log.Printf("[邮件内容同步] 超时处理完成，账号 %d 的processing_status将被重置为0", account.ID)

			// 根据具体原因返回不同的错误
			if hasDeadline && currentTime.After(safeTimeLimit) {
				return successCount, fmt.Errorf("达到安全时限，提前停止处理")
			}
			return successCount, ctx.Err()
		}

		emailStartTime := time.Now()
		email, err := mailClient.GetEmailContent(uint32(emailOne.EmailID), folder)
		emailDuration := time.Since(emailStartTime)
		totalFetchTime += emailDuration

		if err != nil {
			log.Printf("[邮件内容同步] 获取邮件内容失败，邮件ID: %d, 耗时: %v, 错误: %v", emailOne.EmailID, emailDuration, err)
			failureCount++

			// 根据错误类型决定状态：
			// - 网络/连接错误 → -1（重新处理）
			// - 邮件已删除 → -3（已删除）
			// - 其他错误 → -2（永久失败）
			var newStatus int
			errStr := strings.ToLower(err.Error())

			// 检查是否是邮件已删除或UID无效的错误
			if strings.Contains(errStr, "邮件不存在") ||
				strings.Contains(errStr, "邮件uid无效") ||
				strings.Contains(errStr, "bad sequence") {
				newStatus = -3 // 已删除
				log.Printf("[邮件内容同步] 检测到邮件已删除或UID无效，标记为已删除状态: 邮件ID=%d", emailOne.EmailID)
			} else if strings.Contains(errStr, "timeout") ||
				strings.Contains(errStr, "connection") ||
				strings.Contains(errStr, "network") ||
				strings.Contains(errStr, "read tcp") ||
				strings.Contains(errStr, "write tcp") ||
				strings.Contains(errStr, "broken pipe") ||
				strings.Contains(errStr, "connection reset") ||
				strings.Contains(errStr, "i/o timeout") ||
				strings.Contains(errStr, "operation timed out") ||
				strings.Contains(errStr, "context deadline exceeded") ||
				strings.Contains(errStr, "context canceled") ||
				strings.Contains(errStr, "error reading response") ||
				strings.Contains(errStr, "server error") ||
				strings.Contains(errStr, "temporary failure") ||
				strings.Contains(errStr, "service unavailable") ||
				strings.Contains(errStr, "server busy") ||
				strings.Contains(errStr, "please try again later") ||
				strings.Contains(errStr, "连接状态异常") ||
				strings.Contains(errStr, "需要重新建立连接") {
				newStatus = -1 // 重新处理
				log.Printf("[邮件内容同步] 检测到临时错误，设置状态为-1（重新处理），邮件ID: %d", emailOne.EmailID)
			} else {
				newStatus = -2 // 永久失败
				log.Printf("[邮件内容同步] 检测到永久错误，设置状态为-2（永久失败），邮件ID: %d", emailOne.EmailID)
			}

			resetErr := resetEmailStatus(emailOne.EmailID, newStatus)
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
			EmailID:     emailOne.EmailID,
			AccountId:   account.ID,
			Subject:     utils.SanitizeUTF8(email.Subject),
			FromEmail:   utils.SanitizeUTF8(email.From),
			ToEmail:     utils.SanitizeUTF8(email.To),
			Date:        utils.SanitizeUTF8(email.Date),
			Content:     utils.SanitizeUTF8(email.Body),
			HTMLContent: utils.SanitizeUTF8(email.BodyHTML),
			Type:        0,
			Status:      -1,
			CreatedAt:   utils.JsonTime{Time: time.Now()},
		}

		// 查询对应的PrimeEmail记录，以获取HasAttachment值
		var primeEmail model.PrimeEmail
		if err := db.DB().Where("email_id = ? AND account_id = ?", emailOne.EmailID, account.ID).First(&primeEmail).Error; err != nil {
			log.Printf("[邮件内容同步] 查询PrimeEmail记录失败，使用默认附件状态: %v", err)
			// 如果查询失败，则使用默认的附件检测逻辑
			if len(email.Attachments) > 0 {
				emailContent.HasAttachment = 1
			} else {
				emailContent.HasAttachment = 0
			}
		} else {
			// 使用PrimeEmail表中的HasAttachment值
			emailContent.HasAttachment = primeEmail.HasAttachment
			log.Printf("[邮件内容同步] 使用PrimeEmail记录的附件状态，邮件ID: %d, HasAttachment: %d",
				emailOne.EmailID, primeEmail.HasAttachment)
		}

		// 处理附件 - 仅在PrimeEmail表示有附件时处理
		var attachments []*model.PrimeEmailContentAttachment
		var attachmentOSSTime time.Duration

		// 如果PrimeEmail表示没有附件，则跳过附件处理，不需要再检查实际邮件
		if emailContent.HasAttachment == 0 {
			log.Printf("[邮件内容同步] 根据PrimeEmail记录判断邮件无附件，跳过附件处理，邮件ID: %d", emailOne.EmailID)
		} else if len(email.Attachments) > 0 {
			log.Printf("[邮件内容同步] 邮件含有 %d 个附件，邮件ID: %d", len(email.Attachments), emailOne.EmailID)

			attachmentCount += len(email.Attachments)

			for i, att := range email.Attachments {
				log.Printf("[附件处理] 开始处理附件 %d/%d，邮件ID: %d, 文件名: %s",
					i+1, len(email.Attachments), emailOne.EmailID, att.Filename)

				if att.Base64Data != "" {
					// 检查是否为压缩包文件
					if isArchiveFile(att.Filename) {
						// 处理压缩包文件
						log.Printf("[附件处理] 检测到压缩包文件，开始解压处理，邮件ID: %d, 文件名: %s", emailOne.EmailID, att.Filename)
						archiveStartTime := time.Now()

						processedAttachments, archiveErr := processArchiveAttachment(att, int64(emailOne.EmailID), uint(account.ID))
						archiveDuration := time.Since(archiveStartTime)
						attachmentOSSTime += archiveDuration

						if archiveErr != nil {
							log.Printf("[附件处理] 压缩包处理失败，邮件ID: %d, 文件名: %s, 错误: %v",
								emailOne.EmailID, att.Filename, archiveErr)
						} else if len(processedAttachments) > 0 {
							// 压缩包处理成功，为每个解压出来的文件创建附件记录
							log.Printf("[附件处理] 压缩包处理成功，共上传 %d 个文件，总耗时: %v，邮件ID: %d, 文件名: %s",
								len(processedAttachments), archiveDuration, emailOne.EmailID, att.Filename)

							for _, processedAtt := range processedAttachments {
								attachment := &model.PrimeEmailContentAttachment{
									EmailID:   emailOne.EmailID,
									AccountId: account.ID,
									FileName:  utils.SanitizeUTF8(processedAtt.FileName),
									SizeKb:    processedAtt.SizeKB,
									MimeType:  utils.SanitizeUTF8(processedAtt.MimeType),
									OssUrl:    utils.SanitizeUTF8(processedAtt.OssURL),
									CreatedAt: utils.JsonTime{Time: time.Now()},
								}
								attachments = append(attachments, attachment)
							}
						} else {
							log.Printf("[附件处理] 压缩包处理完成但没有成功上传任何文件，邮件ID: %d, 文件名: %s",
								emailOne.EmailID, att.Filename)
						}

						// 无论压缩包处理是否成功，都为原始压缩包文件创建一个附件记录
						originalOssURL := ""
						if archiveErr != nil || len(processedAttachments) == 0 {
							// 如果压缩包处理失败或没有成功上传任何文件，尝试上传原始压缩包
							log.Printf("[附件处理] 上传原始压缩包文件，邮件ID: %d, 文件名: %s",
								emailOne.EmailID, att.Filename)

							fileType := ""
							if att.MimeType != "" {
								parts := strings.Split(att.MimeType, "/")
								if len(parts) > 1 {
									fileType = parts[1]
								}
							}

							// 上传原始压缩包的逻辑（使用封装的重试函数）
							ossStartTime := time.Now()
							originalOssURL, err = uploadWithRetry(att.Filename, att.Base64Data, fileType, emailOne.EmailID, "附件处理")
							ossDuration := time.Since(ossStartTime)
							attachmentOSSTime += ossDuration
							if err != nil {
								log.Printf("[附件处理] 原始压缩包上传失败，邮件ID: %d, 文件名: %s, 错误: %v", emailOne.EmailID, att.Filename, err)
							}
						} else {
							// 压缩包处理成功，也上传原始压缩包作为备份
							log.Printf("[附件处理] 上传原始压缩包文件作为备份，邮件ID: %d, 文件名: %s",
								emailOne.EmailID, att.Filename)

							fileType := ""
							if att.MimeType != "" {
								parts := strings.Split(att.MimeType, "/")
								if len(parts) > 1 {
									fileType = parts[1]
								}
							}

							ossStartTime := time.Now()
							originalOssURL, err = uploadWithRetry(att.Filename, att.Base64Data, fileType, emailOne.EmailID, "附件处理")
							ossDuration := time.Since(ossStartTime)
							attachmentOSSTime += ossDuration
							if err != nil {
								log.Printf("[附件处理] 原始压缩包上传失败，邮件ID: %d, 文件名: %s, 错误: %v", emailOne.EmailID, att.Filename, err)
							}
						}

						// 创建原始压缩包的附件记录
						if originalOssURL != "" {
							originalAttachment := &model.PrimeEmailContentAttachment{
								EmailID:   emailOne.EmailID,
								AccountId: account.ID,
								FileName:  utils.SanitizeUTF8(att.Filename),
								SizeKb:    att.SizeKB,
								MimeType:  utils.SanitizeUTF8(att.MimeType),
								OssUrl:    utils.SanitizeUTF8(originalOssURL),
								CreatedAt: utils.JsonTime{Time: time.Now()},
							}
							attachments = append(attachments, originalAttachment)
						}
					} else {
						// 处理普通附件文件（保持原有逻辑）
						fileType := ""
						if att.MimeType != "" {
							parts := strings.Split(att.MimeType, "/")
							if len(parts) > 1 {
								fileType = parts[1]
							}
						}

						// 使用封装的重试上传函数
						ossStartTime := time.Now()
						ossURL, err := uploadWithRetry(att.Filename, att.Base64Data, fileType, emailOne.EmailID, "附件处理")
						ossDuration := time.Since(ossStartTime)
						attachmentOSSTime += ossDuration
						if err != nil {
							log.Printf("[附件处理] 普通附件上传最终失败，邮件ID: %d, 文件名: %s, 错误: %v", emailOne.EmailID, att.Filename, err)
						}

						// 创建普通附件记录
						attachment := &model.PrimeEmailContentAttachment{
							EmailID:   emailOne.EmailID,
							AccountId: account.ID,
							FileName:  utils.SanitizeUTF8(att.Filename),
							SizeKb:    att.SizeKB,
							MimeType:  utils.SanitizeUTF8(att.MimeType),
							OssUrl:    utils.SanitizeUTF8(ossURL),
							CreatedAt: utils.JsonTime{Time: time.Now()},
						}
						attachments = append(attachments, attachment)
					}
				} else {
					log.Printf("[附件处理] 附件没有Base64数据，跳过创建附件记录，邮件ID: %d, 文件名: %s", emailOne.EmailID, att.Filename)
				}
			}
		} else {
			log.Printf("[邮件内容同步] 邮件没有附件，邮件ID: %d", emailOne.EmailID)
		}

		totalOSSTime += attachmentOSSTime

		// 添加到批量处理列表
		allEmailData = append(allEmailData, EmailContentData{
			EmailID:      emailOne.EmailID,
			AccountId:    account.ID,
			EmailContent: emailContent,
			Attachments:  attachments,
		})

		successCount++
		totalEmailTime := emailDuration + attachmentOSSTime
		log.Printf("[邮件内容同步] 邮件 ID: %d 内容获取成功，获取耗时: %v，OSS耗时: %v，总耗时: %v，进度: %d/%d",
			emailOne.EmailID, emailDuration, attachmentOSSTime, totalEmailTime, i+1, len(accountEmails))
	}

	// 批量保存所有邮件内容和附件
	totalDuration := time.Since(startTime)

	// 详细的性能统计
	if successCount > 0 {
		avgFetchTime := totalFetchTime / time.Duration(successCount)
		avgOSSTime := totalOSSTime / time.Duration(max(attachmentCount, 1))
		avgTotalTime := totalDuration / time.Duration(successCount)

		log.Printf("[性能统计] 账号 %d 处理完成 - 成功: %d, 失败: %d, 总耗时: %v",
			account.ID, successCount, failureCount, totalDuration)
		log.Printf("[性能统计] 平均每邮件: %v, 平均获取: %v, 平均OSS: %v, 总附件: %d",
			avgTotalTime, avgFetchTime, avgOSSTime, attachmentCount)
	}

	if len(allEmailData) > 0 {
		saveStartTime := time.Now()
		err := batchSaveEmailContents(allEmailData)
		saveDuration := time.Since(saveStartTime)

		if err != nil {
			log.Printf("[邮件内容同步] 批量保存邮件内容失败: %v", err)
			return 0, fmt.Errorf("批量保存邮件内容失败: %v", err)
		}

		log.Printf("[邮件内容同步] 账号 %d 批量保存完成: 成功 %d 封，失败 %d 封，总耗时: %v，保存耗时: %v",
			account.ID, successCount, failureCount, totalDuration, saveDuration)
	} else {
		log.Printf("[邮件内容同步] 账号 %d 没有邮件需要保存，总耗时: %v",
			account.ID, totalDuration)
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

		// 更新邮件状态：-1（待处理）→ 1（已处理）
		if err := tx.Model(&model.PrimeEmail{}).Where("email_id = ?", emailData.EmailID).Update("status", 1).Error; err != nil {
			log.Printf("[批量保存邮件内容] 更新邮件状态失败: EmailID=%d, status: -1 → 1, 错误=%v", emailData.EmailID, err)
		} else {
			log.Printf("[批量保存邮件内容] 邮件状态更新成功: EmailID=%d, status: -1 → 1", emailData.EmailID)
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

// max 返回两个整数中的最大值
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ExtractedFile 表示从压缩包中解压出的文件
type ExtractedFile struct {
	Name string
	Data []byte
}

// isArchiveFile 判断文件是否为支持的压缩包格式
func isArchiveFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".zip" || ext == ".rar"
}

// extractZipFiles 解压ZIP文件并返回所有文件内容
func extractZipFiles(base64Data string) ([]ExtractedFile, error) {
	// 解码Base64数据
	zipData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("解码Base64数据失败: %v", err)
	}

	// 创建ZIP reader
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("创建ZIP reader失败: %v", err)
	}

	var extractedFiles []ExtractedFile

	// 遍历ZIP文件中的所有文件
	for _, file := range reader.File {
		// 跳过目录
		if file.FileInfo().IsDir() {
			continue
		}

		// 打开文件
		rc, err := file.Open()
		if err != nil {
			log.Printf("打开ZIP文件 %s 失败: %v", file.Name, err)
			continue
		}

		// 读取文件内容
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			log.Printf("读取ZIP文件 %s 内容失败: %v", file.Name, err)
			continue
		}

		extractedFiles = append(extractedFiles, ExtractedFile{
			Name: file.Name,
			Data: data,
		})
	}

	return extractedFiles, nil
}

// extractRarFiles 解压RAR文件并返回所有文件内容
func extractRarFiles(base64Data string) ([]ExtractedFile, error) {
	// 解码Base64数据
	rarData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("解码Base64数据失败: %v", err)
	}

	// 创建RAR reader
	reader, err := rardecode.NewReader(bytes.NewReader(rarData))
	if err != nil {
		return nil, fmt.Errorf("创建RAR reader失败: %v", err)
	}

	var extractedFiles []ExtractedFile

	// 遍历RAR文件中的所有文件
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("读取RAR文件头失败: %v", err)
			break
		}

		// 跳过目录
		if header.IsDir {
			continue
		}

		// 读取文件内容
		data, err := io.ReadAll(reader)
		if err != nil {
			log.Printf("读取RAR文件 %s 内容失败: %v", header.Name, err)
			continue
		}

		extractedFiles = append(extractedFiles, ExtractedFile{
			Name: header.Name,
			Data: data,
		})
	}

	return extractedFiles, nil
}

// processArchiveAttachment 处理压缩包附件，解压并上传所有文件
// ProcessedAttachment 表示处理后的附件信息
type ProcessedAttachment struct {
	FileName string
	SizeKB   float64
	MimeType string
	OssURL   string
}

func processArchiveAttachment(attachment mailclient.AttachmentInfo, emailID int64, accountID uint) ([]ProcessedAttachment, error) {
	var extractedFiles []ExtractedFile
	var err error

	// 根据文件扩展名选择解压方法
	ext := strings.ToLower(filepath.Ext(attachment.Filename))
	switch ext {
	case ".zip":
		log.Printf("[压缩包处理] 开始解压ZIP文件，邮件ID: %d, 文件名: %s", emailID, attachment.Filename)
		extractedFiles, err = extractZipFiles(attachment.Base64Data)
	case ".rar":
		log.Printf("[压缩包处理] 开始解压RAR文件，邮件ID: %d, 文件名: %s", emailID, attachment.Filename)
		extractedFiles, err = extractRarFiles(attachment.Base64Data)
	default:
		return nil, fmt.Errorf("不支持的压缩包格式: %s", ext)
	}

	if err != nil {
		return nil, fmt.Errorf("解压压缩包失败: %v", err)
	}

	log.Printf("[压缩包处理] 成功解压压缩包，共提取到 %d 个文件，邮件ID: %d, 压缩包: %s",
		len(extractedFiles), emailID, attachment.Filename)

	var processedAttachments []ProcessedAttachment

	// 遍历所有解压出的文件并上传到OSS
	for i, file := range extractedFiles {
		log.Printf("[压缩包处理] 开始上传解压文件 %d/%d，邮件ID: %d, 原压缩包: %s, 文件: %s",
			i+1, len(extractedFiles), emailID, attachment.Filename, file.Name)

		// 将文件数据编码为Base64
		fileBase64 := base64.StdEncoding.EncodeToString(file.Data)

		// 获取文件扩展名作为文件类型
		fileType := strings.TrimPrefix(filepath.Ext(file.Name), ".")
		if fileType == "" {
			fileType = "bin" // 默认为二进制文件
		}

		// 为解压文件生成新的文件名，包含原压缩包名
		archiveName := strings.TrimSuffix(attachment.Filename, filepath.Ext(attachment.Filename))
		newFileName := fmt.Sprintf("%s_%s", archiveName, file.Name)

		// 使用封装的重试上传函数
		ossURL, uploadErr := uploadWithRetry(newFileName, fileBase64, fileType, int(emailID), "压缩包处理")
		if uploadErr == nil {
			// 计算文件大小（KB）
			sizeKB := float64(len(file.Data)) / 1024.0

			// 根据文件扩展名推断MIME类型
			mimeType := getMimeTypeByExtension(file.Name)

			processedAttachment := ProcessedAttachment{
				FileName: newFileName,
				SizeKB:   sizeKB,
				MimeType: mimeType,
				OssURL:   ossURL,
			}
			processedAttachments = append(processedAttachments, processedAttachment)
		} else {
			log.Printf("[压缩包处理] 解压文件上传失败，邮件ID: %d, 文件: %s, 错误: %v",
				emailID, file.Name, uploadErr)
		}
	}

	log.Printf("[压缩包处理] 压缩包处理完成，邮件ID: %d, 压缩包: %s, 成功上传: %d/%d",
		emailID, attachment.Filename, len(processedAttachments), len(extractedFiles))

	return processedAttachments, nil
}

// getMimeTypeByExtension 根据文件扩展名推断MIME类型
func getMimeTypeByExtension(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt":
		return "text/plain"
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".zip":
		return "application/zip"
	case ".rar":
		return "application/x-rar-compressed"
	case ".7z":
		return "application/x-7z-compressed"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".avi":
		return "video/x-msvideo"
	default:
		return "application/octet-stream"
	}
}
