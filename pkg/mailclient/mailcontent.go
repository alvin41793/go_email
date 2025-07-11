package mailclient

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
)

// 中文编码解码器的导入 - 临时解决方案
var (
	gbkDecoder = func() transform.Transformer {
		// 动态导入以避免循环导入
		return nil // 将在运行时设置
	}
	gb18030Decoder = func() transform.Transformer {
		return nil // 将在运行时设置
	}
)

// getGBKDecoder 获取GBK解码器
func getGBKDecoder() transform.Transformer {
	// 这里我们直接使用字符串来避免循环导入问题
	// 在实际使用中，这将通过反射或其他方式解决
	e, _ := ianaindex.MIME.Encoding("gbk")
	if e != nil {
		return e.NewDecoder()
	}
	// 备用方案：返回nil将使用原始输入
	return transform.Nop
}

// getGB18030Decoder 获取GB18030解码器
func getGB18030Decoder() transform.Transformer {
	e, _ := ianaindex.MIME.Encoding("gb18030")
	if e != nil {
		return e.NewDecoder()
	}
	return transform.Nop
}

// DecodeMIMESubject 解码MIME编码的邮件主题 (公共函数用于测试)
func DecodeMIMESubject(subject string) string {
	if subject == "" {
		return subject
	}

	// 使用WordDecoder解码RFC 2047编码的主题
	decoder := &mime.WordDecoder{
		CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
			// 处理常见的中文字符集别名
			switch strings.ToLower(charset) {
			case "gb2312", "gb_2312", "gb_2312-80":
				// 使用GBK解码器来处理GB2312（GBK是GB2312的超集）
				return transform.NewReader(input, getGBKDecoder()), nil
			case "gbk":
				return transform.NewReader(input, getGBKDecoder()), nil
			case "gb18030":
				return transform.NewReader(input, getGB18030Decoder()), nil
			}

			// 尝试使用golang.org/x/text/encoding/ianaindex来处理其他字符集
			e, err := ianaindex.MIME.Encoding(charset)
			if err != nil || e == nil {
				// 如果找不到编码，返回输入流（可能是ASCII或UTF-8）
				return input, nil
			}

			// 使用找到的编码器将输入转换为UTF-8
			return transform.NewReader(input, e.NewDecoder()), nil
		},
	}

	decoded, err := decoder.DecodeHeader(subject)
	if err != nil {
		log.Printf("解码邮件主题失败: %v, 原始主题: %s", err, subject)
		return subject // 如果解码失败，返回原始主题
	}

	return decoded
}

// ListEmails 获取邮件列表
func (m *MailClient) ListEmails(folder string, limit int, fromUID ...uint32) ([]EmailInfo, error) {
	return m.listEmailsWithRetry(folder, limit, 5, fromUID...)
}

// 带重试的获取邮件列表
func (m *MailClient) listEmailsWithRetry(folder string, limit int, maxRetries int, fromUID ...uint32) ([]EmailInfo, error) {
	if folder == "" {
		folder = "INBOX"
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		emails, err := m.tryListEmails(folder, limit, fromUID...)
		if err == nil {
			return emails, nil
		}

		// 检查是否是连接相关的错误（包括包装的错误）
		if isConnectionError(err) || isWrappedConnectionError(err) {
			log.Printf("[邮件列表] 连接错误 (尝试 %d/%d): 文件夹=%s, 错误: %v", attempt, maxRetries, folder, err)
			if attempt < maxRetries {
				// 强制关闭当前连接，下次会重新创建
				globalPool.CloseConnection(m.Config.EmailAddress)
				// 增加重试延迟，使用指数退避策略
				delay := time.Second * time.Duration(attempt*2)
				log.Printf("[邮件列表] 等待 %v 后重试", delay)
				time.Sleep(delay)
				continue
			}
		}

		// 非连接错误，直接返回
		log.Printf("[邮件列表] 非连接错误，直接返回: %v", err)
		return nil, err
	}

	return nil, fmt.Errorf("获取邮件列表失败，已重试 %d 次", maxRetries)
}

// 尝试获取邮件列表（单次）
func (m *MailClient) tryListEmails(folder string, limit int, fromUID ...uint32) ([]EmailInfo, error) {
	// 连接IMAP服务器
	c, err := m.ConnectIMAP()
	if err != nil {
		return nil, err
	}
	defer func() {
		// 不要在这里关闭连接，让连接池管理
		// c.Logout()
	}()

	// 验证连接状态
	state := c.State()
	log.Printf("[邮件列表] 连接状态: %v, 文件夹: %s", state, folder)

	// 确保连接处于正确的状态 (Auth=2 或 Selected=3)
	if state != 2 && state != 3 {
		return nil, fmt.Errorf("连接状态异常: %v，需要重新建立连接", state)
	}

	// 选择邮箱
	mbox, err := c.Select(folder, false)
	if err != nil {
		// 检查是否是IMAP命令错误
		if strings.Contains(strings.ToLower(err.Error()), "command is not a valid imap command") {
			log.Printf("[邮件列表] 检测到IMAP命令错误，重置连接: %v", err)
			// 重置连接状态
			globalPool.ResetConnection(m.Config.EmailAddress)
			return nil, fmt.Errorf("IMAP命令错误，已重置连接: %w", err)
		}
		return nil, fmt.Errorf("选择邮箱失败: %w", err)
	}

	// 如果邮箱中没有邮件，返回空列表
	if mbox.Messages == 0 {
		return []EmailInfo{}, nil
	}

	// 创建序列集
	seqSet := new(imap.SeqSet)

	// 如果指定了起始UID，则使用UID范围
	if len(fromUID) > 0 && fromUID[0] > 0 {
		var endUID uint32
		if len(fromUID) > 1 {
			endUID = fromUID[1]
		} else {
			endUID = fromUID[0] + uint32(limit) // 如果没有指定结束UID，计算一个
		}
		seqSet.AddRange(fromUID[0], endUID)

		// 用UID搜索命令获取消息
		ids, err := c.UidSearch(&imap.SearchCriteria{Uid: seqSet})
		if err != nil {
			return nil, fmt.Errorf("UID搜索失败: %w", err)
		}

		if len(ids) == 0 {
			return []EmailInfo{}, nil
		}

		// 重建序列集用于获取
		seqSet = new(imap.SeqSet)
		seqSet.AddNum(ids...)
	} else {
		// 默认行为：获取最新的邮件
		start := uint32(1)
		if mbox.Messages > uint32(limit) {
			start = mbox.Messages - uint32(limit) + 1
		}
		seqSet.AddRange(start, mbox.Messages)
	}

	// 获取邮件信息
	messages := make(chan *imap.Message, limit)
	done := make(chan error, 1)

	go func() {
		done <- c.Fetch(seqSet, []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchBodyStructure, imap.FetchUid}, messages)
	}()

	var emails []EmailInfo
	for msg := range messages {
		hasAttachments := false

		// 检查是否有附件
		if msg.BodyStructure != nil {
			var checkAttachments func(parts []*imap.BodyStructure) bool
			checkAttachments = func(parts []*imap.BodyStructure) bool {
				for _, part := range parts {
					if part.Disposition == "attachment" || part.Disposition == "inline" && part.Params["filename"] != "" {
						return true
					}
					if part.MIMEType == "multipart" {
						if checkAttachments(part.Parts) {
							return true
						}
					}
				}
				return false
			}

			if msg.BodyStructure.MIMEType == "multipart" {
				hasAttachments = checkAttachments(msg.BodyStructure.Parts)
			} else if msg.BodyStructure.Disposition == "attachment" {
				hasAttachments = true
			}
		}

		info := EmailInfo{
			EmailID:        fmt.Sprint(msg.Uid),
			Subject:        DecodeMIMESubject(msg.Envelope.Subject),
			From:           parseAddressList(msg.Envelope.From),
			Date:           msg.Envelope.Date.Format(time.RFC1123Z),
			UID:            msg.Uid,
			HasAttachments: hasAttachments,
		}
		emails = append(emails, info)
	}

	if err := <-done; err != nil {
		return nil, fmt.Errorf("获取邮件失败: %w", err)
	}

	// 反转邮件列表，使最新的邮件在前面
	for i, j := 0, len(emails)-1; i < j; i, j = i+1, j-1 {
		emails[i], emails[j] = emails[j], emails[i]
	}

	return emails, nil
}

// GetEmailContent 获取邮件完整内容
func (m *MailClient) GetEmailContent(uid uint32, folder string) (*Email, error) {
	return m.getEmailContentWithRetry(uid, folder, 5)
}

// 带重试的获取邮件内容
func (m *MailClient) getEmailContentWithRetry(uid uint32, folder string, maxRetries int) (*Email, error) {
	if folder == "" {
		folder = "INBOX"
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		email, err := m.tryGetEmailContent(uid, folder)
		if err == nil {
			return email, nil
		}

		// 检查是否是连接相关的错误（包括包装的错误）
		if isConnectionError(err) || isWrappedConnectionError(err) {
			log.Printf("[邮件获取] 连接错误 (尝试 %d/%d): UID=%d, 错误: %v", attempt, maxRetries, uid, err)
			if attempt < maxRetries {
				// 强制关闭当前连接，下次会重新创建
				globalPool.CloseConnection(m.Config.EmailAddress)
				// 增加重试延迟，使用指数退避策略
				delay := time.Second * time.Duration(attempt*2)
				log.Printf("[邮件获取] 等待 %v 后重试", delay)
				time.Sleep(delay)
				continue
			}
		}

		// 非连接错误，直接返回
		log.Printf("[邮件获取] 非连接错误，直接返回: %v", err)
		return nil, err
	}

	return nil, fmt.Errorf("获取邮件内容失败，已重试 %d 次", maxRetries)
}

// 尝试获取邮件内容（单次）
func (m *MailClient) tryGetEmailContent(uid uint32, folder string) (*Email, error) {
	// 连接IMAP服务器
	c, err := m.ConnectIMAP()
	if err != nil {
		return nil, err
	}
	defer func() {
		// 不要在这里关闭连接，让连接池管理
		// c.Logout()
	}()

	// 验证连接状态
	state := c.State()
	log.Printf("[邮件获取] 连接状态: %v, UID: %d", state, uid)

	// 确保连接处于正确的状态 (Auth=2 或 Selected=3)
	if state != 2 && state != 3 {
		return nil, fmt.Errorf("连接状态异常: %v，需要重新建立连接", state)
	}

	// 选择邮箱
	mbox, err := c.Select(folder, false)
	if err != nil {
		// 检查是否是IMAP命令错误
		if strings.Contains(strings.ToLower(err.Error()), "command is not a valid imap command") {
			log.Printf("[邮件获取] 检测到IMAP命令错误，重置连接: %v", err)
			// 重置连接状态
			globalPool.ResetConnection(m.Config.EmailAddress)
			return nil, fmt.Errorf("IMAP命令错误，已重置连接: %w", err)
		}
		return nil, fmt.Errorf("选择邮箱失败: %w", err)
	}

	// 检查邮箱是否有邮件
	if mbox.Messages == 0 {
		return nil, fmt.Errorf("邮箱中没有邮件")
	}

	// 验证UID是否有效 - 先检查UID是否存在
	log.Printf("[邮件获取] 验证UID %d 是否存在，邮箱总邮件数: %d", uid, mbox.Messages)

	// 创建搜索条件来验证UID是否存在
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddNum(uid)

	// 先搜索确认UID是否存在
	ids, err := c.UidSearch(criteria)
	if err != nil {
		log.Printf("[邮件获取] UID搜索失败: UID=%d, 错误: %v", uid, err)
		return nil, fmt.Errorf("搜索邮件失败: %w", err)
	}

	if len(ids) == 0 {
		log.Printf("[邮件获取] UID不存在: UID=%d, 邮箱: %s", uid, folder)
		return nil, fmt.Errorf("邮件不存在: UID=%d 在邮箱 %s 中未找到", uid, folder)
	}

	log.Printf("[邮件获取] UID验证成功: UID=%d 存在", uid)

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	// 获取完整邮件，包括正文和附件信息
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchBodyStructure, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqSet, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		// 检查是否是FETCH相关的错误
		if strings.Contains(strings.ToLower(err.Error()), "bad sequence") {
			log.Printf("[邮件获取] 检测到FETCH序列错误: UID=%d, 错误: %v", uid, err)
			return nil, fmt.Errorf("邮件UID无效或已过期: UID=%d, 错误: %w", uid, err)
		}
		return nil, fmt.Errorf("获取邮件内容失败: %w", err)
	}

	if msg == nil {
		log.Printf("[邮件获取] 邮件消息为空: UID=%d", uid)
		return nil, fmt.Errorf("邮件不存在或已被删除: UID=%d", uid)
	}

	// 创建Email结构体
	email := &Email{
		EmailID:     fmt.Sprint(msg.SeqNum),
		Subject:     DecodeMIMESubject(msg.Envelope.Subject),
		From:        parseAddressList(msg.Envelope.From),
		To:          parseAddressList(msg.Envelope.To),
		Date:        msg.Envelope.Date.Format(time.RFC1123Z),
		Attachments: []AttachmentInfo{},
	}

	// 获取完整邮件内容
	r := msg.GetBody(section)
	if r == nil {
		return nil, fmt.Errorf("邮件正文为空")
	}

	// 将io.Reader转换为string
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, fmt.Errorf("读取邮件内容失败: %w", err)
	}
	rawContent := buf.String()

	// 调试输出
	log.Printf("[邮件解析调试] UID: %d, 解码成功，内容长度: %d", uid, len(rawContent))

	//// 保存原始内容到文件用于调试
	//if err := saveRawContentToFile(uid, rawContent); err != nil {
	//	log.Printf("[邮件解析调试] 保存原始内容失败: %v", err)
	//}

	// 解析邮件内容
	if msg.BodyStructure.MIMEType == "multipart" {
		// 多部分邮件
		reader := strings.NewReader(rawContent)
		err = m.parseMultipartMessage(msg, email, reader)
		if err != nil {
			log.Printf("[邮件解析] 解析多部分邮件失败: %v", err)
			// 即使解析失败，也返回基本信息
		}
	} else {
		// 单部分邮件
		email.Body = rawContent
	}

	return email, nil
}

// saveRawContentToFile 将原始邮件内容保存到文件中
func saveRawContentToFile(uid uint32, content string) error {
	// 确保存储目录存在
	emailDir := "email_raw_content"
	if err := os.MkdirAll(emailDir, 0755); err != nil {
		return fmt.Errorf("创建邮件内容目录失败: %w", err)
	}

	// 使用UID和时间戳创建文件名，确保唯一性
	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(emailDir, fmt.Sprintf("email_%d_%s.eml", uid, timestamp))

	// 写入文件
	err := os.WriteFile(filename, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("写入邮件内容到文件失败: %w", err)
	}

	log.Printf("已保存原始邮件内容到文件: %s", filename)
	return nil
}

// parseMultipartMessage 解析多部分邮件
func (m *MailClient) parseMultipartMessage(msg *imap.Message, email *Email, reader io.Reader) error {
	// 使用mail包解析邮件
	mr, err := mail.ReadMessage(reader)
	if err != nil {
		return fmt.Errorf("读取邮件内容失败: %v", err)
	}

	// 获取媒体类型
	contentType := mr.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("解析Content-Type失败: %v", err)
	}

	// 处理多部分邮件
	if strings.HasPrefix(mediaType, "multipart/") {
		// 创建一个递归函数来处理嵌套的多部分邮件
		var parseMultipart func(reader io.Reader, boundary string, depth int) error
		parseMultipart = func(reader io.Reader, boundary string, depth int) error {
			mr := multipart.NewReader(reader, boundary)

			// 遍历每个部分
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					if depth == 0 {
						return fmt.Errorf("读取下一部分失败: %v", err)
					}
					// 对于嵌套部分的错误，我们只记录而不中断
					log.Printf("解析嵌套部分失败: %v", err)
					continue
				}

				// 获取此部分的内容类型
				partContentType := p.Header.Get("Content-Type")
				partMediaType, partParams, err := mime.ParseMediaType(partContentType)
				if err != nil {
					continue // 跳过无法解析类型的部分
				}

				// 处理嵌套的多部分邮件
				if strings.HasPrefix(partMediaType, "multipart/") {
					partBoundary := partParams["boundary"]
					if partBoundary != "" {
						// 递归处理嵌套部分
						bodyBytes, err := io.ReadAll(p)
						if err == nil {
							parseMultipart(bytes.NewReader(bodyBytes), partBoundary, depth+1)
						}
					}
				} else if strings.HasPrefix(partMediaType, "text/plain") {
					// 读取纯文本部分
					bodyBytes, err := io.ReadAll(p)
					if err != nil {
						continue
					}
					// 解码内容
					decodedBody, err := decodeContent(p.Header, bodyBytes)
					if err == nil && decodedBody != "" {
						email.Body = decodedBody
					} else if len(bodyBytes) > 0 {
						email.Body = string(bodyBytes)
					}
				} else if strings.HasPrefix(partMediaType, "text/html") {
					// 读取HTML部分
					bodyBytes, err := io.ReadAll(p)
					if err != nil {
						continue
					}
					// 解码内容
					decodedBody, err := decodeContent(p.Header, bodyBytes)
					if err == nil && decodedBody != "" {
						// 清理HTML内容，移除\r\n和多余的空白
						cleanedHTML := cleanHTMLContent(decodedBody)
						email.BodyHTML = cleanedHTML
					} else if len(bodyBytes) > 0 {
						// 清理HTML内容，移除\r\n和多余的空白
						cleanedHTML := cleanHTMLContent(string(bodyBytes))
						email.BodyHTML = cleanedHTML
					}
				} else if disposition := p.Header.Get("Content-Disposition"); strings.HasPrefix(disposition, "attachment") {
					// 处理附件
					_, params, err := mime.ParseMediaType(disposition)
					if err != nil {
						continue
					}

					filename := params["filename"]
					if filename == "" {
						_, contentTypeParams, _ := mime.ParseMediaType(partContentType)
						filename = contentTypeParams["name"]
					}

					if filename != "" {
						// 解码RFC 2047编码的文件名
						decodedFilename := DecodeMIMESubject(filename)

						// 读取附件内容以获取大小
						attachBytes, err := io.ReadAll(p)
						if err != nil {
							log.Printf("读取附件内容失败: %v", err)
							continue
						}

						// 替换\r\n为空字符串
						//attachBytes = bytes.ReplaceAll(attachBytes, []byte("\r\n"), []byte(""))

						email.Attachments = append(email.Attachments, AttachmentInfo{
							Filename:   decodedFilename, // 使用解码后的文件名
							SizeKB:     float64(len(attachBytes)) / 1024.0,
							MimeType:   partMediaType,
							Base64Data: string(attachBytes),
						})
					}
				}
			}
			return nil
		}

		// 使用递归函数处理多部分邮件
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("未找到boundary参数")
		}

		return parseMultipart(mr.Body, boundary, 0)
	} else if strings.HasPrefix(mediaType, "text/plain") {
		// 对于单一的纯文本邮件
		bodyBytes, err := io.ReadAll(mr.Body)
		if err != nil {
			return err
		}
		email.Body = string(bodyBytes)
	} else if strings.HasPrefix(mediaType, "text/html") {
		// 对于单一的HTML邮件
		bodyBytes, err := io.ReadAll(mr.Body)
		if err != nil {
			return err
		}
		// 清理HTML内容
		cleanedHTML := cleanHTMLContent(string(bodyBytes))
		email.BodyHTML = cleanedHTML
	}

	return nil
}

// decodeContent 根据邮件头解码内容
func decodeContent(header textproto.MIMEHeader, content []byte) (string, error) {
	// 处理内容编码
	encoding := header.Get("Content-Transfer-Encoding")
	var reader io.Reader

	switch strings.ToLower(encoding) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, bytes.NewReader(content))
	case "quoted-printable":
		reader = quotedprintable.NewReader(bytes.NewReader(content))
	default:
		reader = bytes.NewReader(content)
	}

	decoded, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	// 处理字符集
	contentType := header.Get("Content-Type")
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return string(decoded), nil
	}

	charset := params["charset"]
	if charset == "" {
		return string(decoded), nil
	}

	// 统一处理所有字符集
	charset = strings.ToLower(charset)
	e, err := ianaindex.MIME.Encoding(charset)
	if err != nil || e == nil {
		return string(decoded), nil
	}

	utf8Reader := transform.NewReader(bytes.NewReader(decoded), e.NewDecoder())
	utf8Content, err := io.ReadAll(utf8Reader)
	if err != nil {
		return string(decoded), nil
	}

	return string(utf8Content), nil
}

// GetAttachment 获取邮件附件
func (m *MailClient) GetAttachment(uid uint32, filename string, folder string) ([]byte, string, error) {
	return m.getAttachmentWithRetry(uid, filename, folder, 5)
}

// 带重试的获取附件
func (m *MailClient) getAttachmentWithRetry(uid uint32, filename string, folder string, maxRetries int) ([]byte, string, error) {
	if folder == "" {
		folder = "INBOX"
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		data, mimeType, err := m.tryGetAttachment(uid, filename, folder)
		if err == nil {
			return data, mimeType, nil
		}

		// 检查是否是连接相关的错误（包括包装的错误）
		if isConnectionError(err) || isWrappedConnectionError(err) {
			log.Printf("[附件获取] 连接错误 (尝试 %d/%d): UID=%d, 文件=%s, 错误: %v", attempt, maxRetries, uid, filename, err)
			if attempt < maxRetries {
				// 强制关闭当前连接，下次会重新创建
				globalPool.CloseConnection(m.Config.EmailAddress)
				// 增加重试延迟，使用指数退避策略
				delay := time.Second * time.Duration(attempt*2)
				log.Printf("[附件获取] 等待 %v 后重试", delay)
				time.Sleep(delay)
				continue
			}
		}

		// 非连接错误，直接返回
		log.Printf("[附件获取] 非连接错误，直接返回: %v", err)
		return nil, "", err
	}

	return nil, "", fmt.Errorf("获取附件失败，已重试 %d 次", maxRetries)
}

// 尝试获取附件（单次）
func (m *MailClient) tryGetAttachment(uid uint32, filename string, folder string) ([]byte, string, error) {
	// 连接IMAP服务器
	c, err := m.ConnectIMAP()
	if err != nil {
		return nil, "", err
	}
	defer func() {
		// 不要在这里关闭连接，让连接池管理
		// c.Logout()
	}()

	// 验证连接状态
	state := c.State()
	log.Printf("[附件获取] 连接状态: %v, UID: %d, 文件: %s", state, uid, filename)

	// 确保连接处于正确的状态 (Auth=2 或 Selected=3)
	if state != 2 && state != 3 {
		return nil, "", fmt.Errorf("连接状态异常: %v，需要重新建立连接", state)
	}

	// 选择邮箱
	_, err = c.Select(folder, false)
	if err != nil {
		// 检查是否是IMAP命令错误
		if strings.Contains(strings.ToLower(err.Error()), "command is not a valid imap command") {
			log.Printf("[附件获取] 检测到IMAP命令错误，重置连接: %v", err)
			// 重置连接状态
			globalPool.ResetConnection(m.Config.EmailAddress)
			return nil, "", fmt.Errorf("IMAP命令错误，已重置连接: %w", err)
		}
		return nil, "", fmt.Errorf("选择邮箱失败: %w", err)
	}

	// 创建搜索条件
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddNum(uid)

	// 搜索邮件（使用 UidSearch 因为我们传入的是 UID）
	ids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, "", fmt.Errorf("搜索邮件失败: %w", err)
	}

	if len(ids) == 0 {
		return nil, "", fmt.Errorf("未找到邮件")
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	// 获取邮件结构
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchBodyStructure, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqSet, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return nil, "", fmt.Errorf("获取邮件结构失败: %w", err)
	}

	if msg == nil {
		return nil, "", fmt.Errorf("邮件不存在")
	}

	// 查找指定的附件
	var attachmentSection *imap.BodySectionName
	var attachmentMimeType string

	var findAttachment func(parts []*imap.BodyStructure, path []int) bool
	findAttachment = func(parts []*imap.BodyStructure, path []int) bool {
		for i, part := range parts {
			currentPath := append(path, i+1)

			// 检查是否是附件
			if part.Disposition == "attachment" || part.Disposition == "inline" {
				attachmentFilename := part.Params["filename"]
				if attachmentFilename == "" {
					attachmentFilename = part.Params["name"]
				}

				if attachmentFilename == filename {
					// 找到了匹配的附件
					attachmentSection = &imap.BodySectionName{
						BodyPartName: imap.BodyPartName{
							Specifier: imap.TextSpecifier,
							Path:      currentPath,
						},
						Peek: true,
					}
					attachmentMimeType = part.MIMEType + "/" + part.MIMESubType
					return true
				}
			}

			// 递归查找子部分
			if part.MIMEType == "multipart" && len(part.Parts) > 0 {
				if findAttachment(part.Parts, currentPath) {
					return true
				}
			}
		}
		return false
	}

	// 开始查找附件
	if msg.BodyStructure.MIMEType == "multipart" {
		found := findAttachment(msg.BodyStructure.Parts, []int{})
		if !found {
			return nil, "", fmt.Errorf("未找到附件: %s", filename)
		}
	} else {
		return nil, "", fmt.Errorf("邮件不包含附件")
	}

	// 再次获取指定附件
	attachItems := []imap.FetchItem{attachmentSection.FetchItem()}
	attachMessages := make(chan *imap.Message, 1)
	attachDone := make(chan error, 1)

	go func() {
		attachDone <- c.UidFetch(seqSet, attachItems, attachMessages)
	}()

	attachMsg := <-attachMessages
	if err := <-attachDone; err != nil {
		return nil, "", fmt.Errorf("获取附件内容失败: %w", err)
	}

	if attachMsg == nil {
		return nil, "", fmt.Errorf("附件不存在")
	}

	// 读取附件内容
	r := attachMsg.GetBody(attachmentSection)
	if r == nil {
		return nil, "", fmt.Errorf("附件内容为空")
	}

	// 读取数据
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("读取附件数据失败: %w", err)
	}

	// 解码base64（如果需要）
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err == nil {
		// 解码成功，使用解码后的数据
		data = decoded
	}
	// 如果解码失败，使用原始数据

	// 如果无法获取MIME类型，使用默认值
	finalMimeType := "application/octet-stream"
	if attachmentMimeType != "" {
		finalMimeType = attachmentMimeType
	}

	return data, finalMimeType, nil
}

// SendEmail 发送邮件
func (m *MailClient) SendEmail(toAddress, subject, body, contentType string) error {
	// 使用smtp包连接服务器
	auth := smtp.PlainAuth("", m.Config.EmailAddress, m.Config.Password, m.Config.SMTPServer)

	// 设置标头
	header := make(map[string]string)
	header["From"] = m.Config.EmailAddress
	header["To"] = toAddress
	header["Subject"] = mime.QEncoding.Encode("utf-8", subject)
	header["MIME-Version"] = "1.0"

	if contentType == "html" {
		header["Content-Type"] = "text/html; charset=UTF-8"
	} else {
		header["Content-Type"] = "text/plain; charset=UTF-8"
	}

	// 构建邮件内容
	message := ""
	for k, v := range header {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + body

	// 连接SMTP服务器并发送
	smtpAddr := fmt.Sprintf("%s:%d", m.Config.SMTPServer, m.Config.SMTPPort)

	// 部分邮件服务器可能需要TLS
	c, err := smtp.Dial(smtpAddr)
	if err != nil {
		return fmt.Errorf("连接SMTP服务器失败: %w", err)
	}
	defer c.Quit()

	if err = c.Hello("localhost"); err != nil {
		return fmt.Errorf("HELO失败: %w", err)
	}

	// 启用TLS
	if ok, _ := c.Extension("STARTTLS"); ok {
		config := &tls.Config{ServerName: m.Config.SMTPServer}
		if err = c.StartTLS(config); err != nil {
			return fmt.Errorf("StartTLS失败: %w", err)
		}
	}

	// 进行身份验证
	if err = c.Auth(auth); err != nil {
		return fmt.Errorf("SMTP认证失败: %w", err)
	}

	// 设置发件人和收件人
	if err = c.Mail(m.Config.EmailAddress); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}

	to := strings.Split(toAddress, ",")
	for _, addr := range to {
		addr = strings.TrimSpace(addr)
		if err = c.Rcpt(addr); err != nil {
			return fmt.Errorf("设置收件人失败: %w", err)
		}
	}

	// 发送邮件内容
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("获取数据写入器失败: %w", err)
	}

	_, err = w.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("写入邮件内容失败: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("关闭数据写入器失败: %w", err)
	}

	return nil
}

// 解析邮件地址列表
func parseAddressList(addresses []*imap.Address) string {
	if len(addresses) == 0 {
		return ""
	}

	var addrList []string
	for _, addr := range addresses {
		if addr.PersonalName != "" {
			// 解码MIME编码的个人姓名
			decodedName := DecodeMIMESubject(addr.PersonalName)
			addrList = append(addrList, fmt.Sprintf("%s <%s@%s>", decodedName, addr.MailboxName, addr.HostName))
		} else {
			addrList = append(addrList, fmt.Sprintf("%s@%s", addr.MailboxName, addr.HostName))
		}
	}

	return strings.Join(addrList, ", ")
}

// extractPlainText 从原始邮件内容中提取纯文本内容
func extractPlainText(content string) string {
	// 查找纯文本部分的标记
	plainStart := strings.Index(content, "Content-Type: text/plain")
	if plainStart < 0 {
		return ""
	}

	// 找到内容部分的开始
	bodyStart := strings.Index(content[plainStart:], "\r\n\r\n")
	if bodyStart < 0 {
		bodyStart = strings.Index(content[plainStart:], "\n\n")
		if bodyStart < 0 {
			return ""
		}
	}

	// 计算实际的内容开始位置
	plainStart += bodyStart

	// 找到下一个边界
	boundary := "--_"
	boundaryPos := strings.Index(content[plainStart:], boundary)

	var plainText string
	if boundaryPos < 0 {
		// 如果找不到下一个边界，就取到末尾
		plainText = content[plainStart:]
	} else {
		// 找到了边界，就取到边界为止
		plainText = content[plainStart : plainStart+boundaryPos]
	}

	// 清理文本
	plainText = strings.TrimSpace(plainText)
	return plainText
}

// extractHTML 从原始邮件内容中提取HTML内容
func extractHTML(content string) string {
	// 查找HTML部分的标记
	htmlStart := strings.Index(content, "Content-Type: text/html")
	if htmlStart < 0 {
		return ""
	}

	// 找到内容部分的开始
	bodyStart := strings.Index(content[htmlStart:], "\r\n\r\n")
	if bodyStart < 0 {
		bodyStart = strings.Index(content[htmlStart:], "\n\n")
		if bodyStart < 0 {
			return ""
		}
	}

	// 计算实际的内容开始位置
	htmlStart += bodyStart

	// 找到下一个边界
	boundary := "--_"
	boundaryPos := strings.Index(content[htmlStart:], boundary)

	var htmlText string
	if boundaryPos < 0 {
		// 如果找不到下一个边界，就取到末尾
		htmlText = content[htmlStart:]
	} else {
		// 找到了边界，就取到边界为止
		htmlText = content[htmlStart : htmlStart+boundaryPos]
	}

	// 清理文本
	htmlText = strings.TrimSpace(htmlText)
	return htmlText
}

// cleanHTMLContent 清理HTML内容，移除\r\n和多余的空白
func cleanHTMLContent(html string) string {
	// 替换\r\n为空
	html = strings.ReplaceAll(html, "\r\n", " ")

	//// 替换连续的空白字符为单个空格
	//re := regexp.MustCompile(`\s+`)
	//html = re.ReplaceAllString(html, " ")

	//// 处理HTML标签之间的不必要空格
	//re = regexp.MustCompile(`>\s+<`)
	//html = re.ReplaceAllString(html, "><")

	return strings.TrimSpace(html)
}

func (m *MailClient) ForwardOriginalEmail(uid uint32, sourceFolder string, toAddress string) error {
	return m.forwardOriginalEmailWithRetry(uid, sourceFolder, toAddress, 5)
}

// 带重试的转发原始邮件
func (m *MailClient) forwardOriginalEmailWithRetry(uid uint32, sourceFolder string, toAddress string, maxRetries int) error {
	if sourceFolder == "" {
		sourceFolder = "INBOX"
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := m.tryForwardOriginalEmail(uid, sourceFolder, toAddress)
		if err == nil {
			return nil
		}

		// 检查是否是连接相关的错误（包括包装的错误）
		if isConnectionError(err) || isWrappedConnectionError(err) {
			log.Printf("[邮件转发] 连接错误 (尝试 %d/%d): UID=%d, 错误: %v", attempt, maxRetries, uid, err)
			if attempt < maxRetries {
				// 强制关闭当前连接，下次会重新创建
				globalPool.CloseConnection(m.Config.EmailAddress)
				// 增加重试延迟，使用指数退避策略
				delay := time.Second * time.Duration(attempt*2)
				log.Printf("[邮件转发] 等待 %v 后重试", delay)
				time.Sleep(delay)
				continue
			}
		}

		// 非连接错误，直接返回
		log.Printf("[邮件转发] 非连接错误，直接返回: %v", err)
		return err
	}

	return fmt.Errorf("转发原始邮件失败，已重试 %d 次", maxRetries)
}

// 尝试转发原始邮件（单次）
func (m *MailClient) tryForwardOriginalEmail(uid uint32, sourceFolder string, toAddress string) error {
	// 连接IMAP服务器
	c, err := m.ConnectIMAP()
	if err != nil {
		return err
	}
	defer func() {
		// 不要在这里关闭连接，让连接池管理
		// c.Logout()
	}()

	// 验证连接状态
	state := c.State()
	log.Printf("[邮件转发] 连接状态: %v, UID: %d", state, uid)

	// 确保连接处于正确的状态 (Auth=2 或 Selected=3)
	if state != 2 && state != 3 {
		return fmt.Errorf("连接状态异常: %v，需要重新建立连接", state)
	}

	// 选择邮箱
	_, err = c.Select(sourceFolder, false)
	if err != nil {
		// 检查是否是IMAP命令错误
		if strings.Contains(strings.ToLower(err.Error()), "command is not a valid imap command") {
			log.Printf("[邮件转发] 检测到IMAP命令错误，重置连接: %v", err)
			// 重置连接状态
			globalPool.ResetConnection(m.Config.EmailAddress)
			return fmt.Errorf("IMAP命令错误，已重置连接: %w", err)
		}
		return fmt.Errorf("选择邮箱失败: %w", err)
	}

	// 创建搜索条件
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddNum(uid)

	// 搜索邮件（使用 UidSearch 因为我们传入的是 UID）
	ids, err := c.UidSearch(criteria)
	if err != nil {
		return fmt.Errorf("搜索邮件失败: %w", err)
	}

	if len(ids) == 0 {
		return fmt.Errorf("未找到邮件")
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	// 获取原始邮件数据
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqSet, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return fmt.Errorf("获取邮件内容失败: %w", err)
	}

	if msg == nil {
		return fmt.Errorf("邮件不存在")
	}

	// 获取邮件正文
	r := msg.GetBody(section)
	if r == nil {
		return fmt.Errorf("邮件正文为空")
	}

	// 读取原始邮件数据
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return fmt.Errorf("读取邮件内容失败: %w", err)
	}
	rawEmail := buf.Bytes()

	// 创建新的MIME邮件
	var newEmail bytes.Buffer

	// 设置邮件头
	fmt.Fprintf(&newEmail, "From: %s\r\n", m.Config.EmailAddress)
	fmt.Fprintf(&newEmail, "To: %s\r\n", toAddress)
	fmt.Fprintf(&newEmail, "Subject: Fwd: %s\r\n", mime.QEncoding.Encode("utf-8", DecodeMIMESubject(msg.Envelope.Subject)))
	fmt.Fprintf(&newEmail, "MIME-Version: 1.0\r\n")

	// 创建多部分邮件
	boundary := "----=_NextPart_" + time.Now().Format("20060102150405")
	fmt.Fprintf(&newEmail, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary)

	// 添加转发前言
	fmt.Fprintf(&newEmail, "--%s\r\n", boundary)
	fmt.Fprintf(&newEmail, "Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(&newEmail, "---------- 转发的原始邮件 ----------\r\n")
	fmt.Fprintf(&newEmail, "发件人: %s\r\n", parseAddressList(msg.Envelope.From))
	fmt.Fprintf(&newEmail, "日期: %s\r\n", msg.Envelope.Date.Format(time.RFC1123Z))
	fmt.Fprintf(&newEmail, "主题: %s\r\n", DecodeMIMESubject(msg.Envelope.Subject))
	fmt.Fprintf(&newEmail, "收件人: %s\r\n\r\n", parseAddressList(msg.Envelope.To))

	// 添加原始邮件作为附件
	fmt.Fprintf(&newEmail, "--%s\r\n", boundary)
	fmt.Fprintf(&newEmail, "Content-Type: message/rfc822\r\n")
	fmt.Fprintf(&newEmail, "Content-Disposition: attachment; filename=\"original_message.eml\"\r\n\r\n")
	newEmail.Write(rawEmail)

	// 结束边界
	fmt.Fprintf(&newEmail, "\r\n--%s--", boundary)

	// 发送邮件
	auth := smtp.PlainAuth("", m.Config.EmailAddress, m.Config.Password, m.Config.SMTPServer)
	err = smtp.SendMail(
		fmt.Sprintf("%s:%d", m.Config.SMTPServer, m.Config.SMTPPort),
		auth,
		m.Config.EmailAddress,
		[]string{toAddress},
		newEmail.Bytes(),
	)

	if err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}

	return nil
}

func (m *MailClient) ForwardStructuredEmail(uid uint32, sourceFolder string, toAddress string) error {
	startTime := time.Now() // 总开始时间

	// 获取原始邮件内容
	fetchStartTime := time.Now()
	email, err := m.GetEmailContent(uid, sourceFolder)
	fetchDuration := time.Since(fetchStartTime)
	log.Printf("[邮件转发详情] 邮件ID: %d, 获取原始邮件内容耗时: %v", uid, fetchDuration)

	if err != nil {
		return fmt.Errorf("获取原始邮件失败: %w", err)
	}

	// 准备转发邮件（email.Subject已经在GetEmailContent中解码过了）
	forwardSubject := "PrimeFwd: " + email.Subject

	// 构建转发邮件
	buildStartTime := time.Now()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// 设置邮件头
	header := make(map[string]string)
	header["From"] = m.Config.EmailAddress
	header["To"] = toAddress
	header["Subject"] = mime.QEncoding.Encode("utf-8", forwardSubject)
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = "multipart/mixed; boundary=" + writer.Boundary()

	// 写入邮件头
	for k, v := range header {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	buf.WriteString("\r\n")

	// 转发头信息
	forwardHeader := fmt.Sprintf(`---------- 转发的邮件 ----------
发件人: %s
日期: %s
主题: %s
收件人: %s

`, email.From, email.Date, email.Subject, email.To)

	// 添加文本部分
	textPart, _ := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type": []string{"text/plain; charset=UTF-8"},
	})
	fmt.Fprint(textPart, forwardHeader+email.Body)

	// 如果有HTML内容，也添加HTML部分
	if email.BodyHTML != "" {
		htmlForwardHeader := strings.ReplaceAll(forwardHeader, "\n", "<br>")
		htmlPart, _ := writer.CreatePart(textproto.MIMEHeader{
			"Content-Type": []string{"text/html; charset=UTF-8"},
		})
		fmt.Fprintf(htmlPart, "<div>%s</div><hr>%s", htmlForwardHeader, email.BodyHTML)
	}

	buildContentDuration := time.Since(buildStartTime)
	log.Printf("[邮件转发详情] 邮件ID: %d, 构建邮件内容耗时: %v", uid, buildContentDuration)

	// 添加所有附件
	attachmentStartTime := time.Now()
	attachmentCount := 0

	for _, attachment := range email.Attachments {
		// 获取附件内容
		data, mimeType, err := m.GetAttachment(uid, attachment.Filename, sourceFolder)
		if err != nil {
			log.Printf("[邮件转发详情] 邮件ID: %d, 获取附件 %s 失败: %v", uid, attachment.Filename, err)
			continue // 如果无法获取，跳过此附件
		}

		// 创建附件部分
		attachmentPart, _ := writer.CreatePart(textproto.MIMEHeader{
			"Content-Type":              []string{mimeType},
			"Content-Disposition":       []string{fmt.Sprintf("attachment; filename=\"%s\"", attachment.Filename)},
			"Content-Transfer-Encoding": []string{"base64"},
		})

		// 写入base64编码的附件数据
		encoder := base64.NewEncoder(base64.StdEncoding, attachmentPart)
		encoder.Write(data)
		encoder.Close()
		attachmentCount++
	}

	attachmentDuration := time.Since(attachmentStartTime)
	log.Printf("[邮件转发详情] 邮件ID: %d, 处理 %d 个附件耗时: %v", uid, attachmentCount, attachmentDuration)

	writer.Close()

	// 发送邮件
	sendStartTime := time.Now()
	auth := smtp.PlainAuth("", m.Config.EmailAddress, m.Config.Password, m.Config.SMTPServer)
	err = smtp.SendMail(
		fmt.Sprintf("%s:%d", m.Config.SMTPServer, m.Config.SMTPPort),
		auth,
		m.Config.EmailAddress,
		[]string{toAddress},
		buf.Bytes(),
	)
	sendDuration := time.Since(sendStartTime)
	log.Printf("[邮件转发详情] 邮件ID: %d, 发送邮件耗时: %v", uid, sendDuration)

	if err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}

	totalDuration := time.Since(startTime)
	log.Printf("[邮件转发详情] 邮件ID: %d, 转发完成, 总耗时: %v (获取: %v, 构建: %v, 附件: %v, 发送: %v)",
		uid, totalDuration, fetchDuration, buildContentDuration, attachmentDuration, sendDuration)

	return nil
}

// 检查是否是包装的连接错误（如 "选择邮箱失败: short write"）
func isWrappedConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// 检查是否包含常见的包装错误前缀和连接错误
	wrappedPrefixes := []string{
		"选择邮箱失败:",
		"搜索邮件失败:",
		"获取邮件失败:",
		"获取邮件内容失败:",
		"获取附件失败:",
		"连接imap服务器失败:",
		"imap登录失败:",
		"获取邮件结构失败:",
		"获取附件内容失败:",
		"读取邮件内容失败:",
		"读取下一部分失败:",
		"读取附件数据失败:",
		"读取响应失败:",
		"发送邮件失败:",
	}

	connectionErrors := []string{
		"short write",
		"connection closed",
		"connection reset",
		"broken pipe",
		"use of closed network connection",
		"read tcp",
		"write tcp",
		"i/o timeout",
		"connection lost",
		"network error",
		"socket closed",
		"timeout",
		"eof",
		"network is unreachable",
		"connection refused",
		"connection timed out",
		"no such host",
		"dial tcp",
		"wsarecv",
		"wsasend",
		"operation timed out",
		"connection aborted",
		"network down",
		"host is down",
		"interrupted system call",
		"broken stream",
		"protocol error",
		"bad file descriptor",
		"operation canceled",
		"context canceled",
		"context deadline exceeded",
		"command is not a valid imap command",
		"imap命令错误",
		"连接状态异常",
		"invalid connection state",
		"connection in wrong state",
		"bad connection",
		"stale connection",
		"connection not ready",
	}

	// 检查是否有包装前缀
	hasWrapperPrefix := false
	for _, prefix := range wrappedPrefixes {
		if strings.Contains(errStr, prefix) {
			hasWrapperPrefix = true
			break
		}
	}

	// 如果有包装前缀，检查是否包含连接错误
	if hasWrapperPrefix {
		for _, connErr := range connectionErrors {
			if strings.Contains(errStr, connErr) {
				return true
			}
		}
	}

	return false
}
