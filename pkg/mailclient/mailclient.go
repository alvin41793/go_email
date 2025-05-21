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
	"strings"
	"time"

	"net/textproto"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
)

// MailClient 结构体，用于处理邮件收发
type MailClient struct {
	IMAPServer   string
	SMTPServer   string
	EmailAddress string
	Password     string
	IMAPPort     int
	SMTPPort     int
	UseSSL       bool
}

// 邮件信息结构体
type EmailInfo struct {
	EmailID        string `json:"email_id"`
	Subject        string `json:"subject"`
	From           string `json:"from"`
	Date           string `json:"date"`
	UID            uint32 `json:"uid"`
	HasAttachments bool   `json:"has_attachments"`
}

// 附件信息结构体
type AttachmentInfo struct {
	Filename   string  `json:"filename"`
	SizeKB     float64 `json:"size_kb"`
	MimeType   string  `json:"mime_type"`
	Base64Data string  `json:"base64_data,omitempty"` // base64编码的附件内容
	OssURL     string  `json:"oss_url,omitempty"`     // OSS存储链接
}

// Email 结构体，包含邮件完整内容
type Email struct {
	EmailID     string           `json:"email_id"`
	Subject     string           `json:"subject"`
	From        string           `json:"from"`
	To          string           `json:"to"`
	Date        string           `json:"date"`
	Body        string           `json:"body"`
	BodyHTML    string           `json:"body_html"`
	Attachments []AttachmentInfo `json:"attachments"`
}

// NewMailClient 创建一个新的邮件客户端
func NewMailClient(imapServer, smtpServer, emailAddress, password string, imapPort, smtpPort int, useSSL bool) *MailClient {
	return &MailClient{
		IMAPServer:   imapServer,
		SMTPServer:   smtpServer,
		EmailAddress: emailAddress,
		password: REDACTED
		IMAPPort:     imapPort,
		SMTPPort:     smtpPort,
		UseSSL:       useSSL,
	}
}

// ConnectIMAP 连接到IMAP服务器
func (m *MailClient) ConnectIMAP() (*client.Client, error) {
	var c *client.Client
	var err error

	// 如果使用SSL，则使用TLS连接
	if m.UseSSL {
		c, err = client.DialTLS(fmt.Sprintf("%s:%d", m.IMAPServer, m.IMAPPort), nil)
	} else {
		c, err = client.Dial(fmt.Sprintf("%s:%d", m.IMAPServer, m.IMAPPort))
		if err == nil {
			if err = c.StartTLS(nil); err != nil {
				c.Logout()
				return nil, fmt.Errorf("StartTLS失败: %w", err)
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("连接IMAP服务器失败: %w", err)
	}

	// 登录
	if err := c.Login(m.EmailAddress, m.Password); err != nil {
		c.Logout()
		return nil, fmt.Errorf("IMAP登录失败: %w", err)
	}

	return c, nil
}

// ListEmails 获取指定文件夹中的邮件列表
func (m *MailClient) ListEmails(folder string, limit int) ([]EmailInfo, error) {
	if folder == "" {
		folder = "INBOX"
	}
	if limit <= 0 {
		limit = 10
	}

	// 连接IMAP服务器
	c, err := m.ConnectIMAP()
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// 选择邮箱
	_, err = c.Select(folder, false)
	if err != nil {
		return nil, fmt.Errorf("选择邮箱失败: %w", err)
	}

	// 搜索所有邮件
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.DeletedFlag}
	ids, err := c.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("搜索邮件失败: %w", err)
	}

	// 如果没有邮件，返回空列表
	if len(ids) == 0 {
		return []EmailInfo{}, nil
	}

	// 限制查询数量
	if len(ids) > limit {
		ids = ids[len(ids)-limit:]
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	// 获取邮件信息（只获取标题等信息，不获取内容）
	messages := make(chan *imap.Message, 10)
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
			EmailID:        fmt.Sprint(msg.SeqNum),
			Subject:        msg.Envelope.Subject,
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
	if folder == "" {
		folder = "INBOX"
	}

	// 连接IMAP服务器
	c, err := m.ConnectIMAP()
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// 选择邮箱
	_, err = c.Select(folder, false)
	if err != nil {
		return nil, fmt.Errorf("选择邮箱失败: %w", err)
	}

	// 创建搜索条件
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddNum(uid)

	// 搜索邮件
	ids, err := c.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("搜索邮件失败: %w", err)
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("未找到邮件")
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	// 获取完整邮件，包括正文和附件信息
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchBodyStructure, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqSet, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return nil, fmt.Errorf("获取邮件内容失败: %w", err)
	}

	if msg == nil {
		return nil, fmt.Errorf("邮件不存在")
	}

	// 创建Email结构体
	email := &Email{
		EmailID:     fmt.Sprint(msg.SeqNum),
		Subject:     msg.Envelope.Subject,
		From:        parseAddressList(msg.Envelope.From),
		To:          parseAddressList(msg.Envelope.To),
		Date:        msg.Envelope.Date.Format(time.RFC1123Z),
		Attachments: []AttachmentInfo{},
	}

	// 获取完整邮件内容
	r := msg.GetBody(section)

	if r != nil {
		// 先保存原始内容，以便出现解析错误时使用
		rawBytes, _ := io.ReadAll(r)
		rawContent := ""
		if len(rawBytes) > 0 {
			rawContent = string(rawBytes)
		}
		fmt.Println("rawContent", rawContent)
		// 尝试获取原始邮件数据进行备用
		// 这是为了保证在解析失败时，我们仍然有数据返回
		email.Body = "无法解析邮件内容，可能是格式复杂或不支持的格式"

		// 如果是简单的文本邮件，直接解析
		if msg.BodyStructure.MIMEType == "text" {
			if msg.BodyStructure.MIMESubType == "plain" {
				email.Body = rawContent
			} else if msg.BodyStructure.MIMESubType == "html" {
				email.BodyHTML = rawContent
			}
		} else if msg.BodyStructure.MIMEType == "multipart" {
			// 对于多部分邮件，使用特殊的解析逻辑
			// 重新构建一个Reader
			r = bytes.NewReader(rawBytes)
			err = m.parseMultipartMessage(msg, email, r)
			if err != nil {
				log.Printf("解析多部分邮件失败: %v", err)

				// 如果解析失败，尝试使用备选方法
				if email.Body == "无法解析邮件内容，可能是格式复杂或不支持的格式" {
					// 尝试提取纯文本内容
					email.Body = extractPlainText(rawContent)
				}

				if email.BodyHTML == "" {
					// 尝试提取HTML内容
					email.BodyHTML = extractHTML(rawContent)
				}
			}
		}

		// 确保至少有一部分内容能够返回
		if (email.Body == "" || email.Body == "无法解析邮件内容，可能是格式复杂或不支持的格式") &&
			(email.BodyHTML == "" || email.BodyHTML == "无法解析HTML内容，邮件可能是复杂格式。") {
			email.Body = extractPlainText(rawContent)
			if email.Body == "" {
				email.Body = "邮件内容解析失败，原始内容:\n" + rawContent
			}
		}
	}

	return email, nil
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
						// 读取附件内容以获取大小
						attachBytes, err := io.ReadAll(p)
						if err != nil {
							log.Printf("读取附件内容失败: %v", err)
							continue
						}

						// 替换\r\n为空字符串
						//attachBytes = bytes.ReplaceAll(attachBytes, []byte("\r\n"), []byte(""))

						email.Attachments = append(email.Attachments, AttachmentInfo{
							Filename:   filename,
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

// 获取特定邮件的特定附件
func (m *MailClient) GetAttachment(uid uint32, filename string, folder string) ([]byte, string, error) {
	if folder == "" {
		folder = "INBOX"
	}

	// 连接IMAP服务器
	c, err := m.ConnectIMAP()
	if err != nil {
		return nil, "", err
	}
	defer c.Logout()

	// 选择邮箱
	_, err = c.Select(folder, false)
	if err != nil {
		return nil, "", fmt.Errorf("选择邮箱失败: %w", err)
	}

	// 创建搜索条件
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddNum(uid)

	// 搜索邮件
	ids, err := c.Search(criteria)
	if err != nil {
		return nil, "", fmt.Errorf("搜索邮件失败: %w", err)
	}

	if len(ids) == 0 {
		return nil, "", fmt.Errorf("未找到邮件")
	}

	// 获取完整邮件内容并解析附件
	email, err := m.GetEmailContent(uid, folder)
	if err != nil {
		return nil, "", fmt.Errorf("获取邮件内容失败: %w", err)
	}

	// 查找指定的附件
	for _, attachment := range email.Attachments {
		if attachment.Filename == filename {
			// 如果已经保存了base64数据，直接解码并返回
			if attachment.Base64Data != "" {
				data, err := base64.StdEncoding.DecodeString(attachment.Base64Data)
				if err != nil {
					return nil, "", fmt.Errorf("解码附件内容失败: %w", err)
				}
				return data, attachment.MimeType, nil
			}
		}
	}

	// 如果在Email对象中找不到附件或base64数据，尝试使用旧方法获取
	// 获取完整邮件
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	// 获取邮件结构
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchBodyStructure, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqSet, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return nil, "", fmt.Errorf("获取邮件内容失败: %w", err)
	}

	if msg == nil || msg.BodyStructure == nil {
		return nil, "", fmt.Errorf("邮件不存在或结构无效")
	}

	// 完整获取邮件内容
	reader := msg.GetBody(section)
	if reader == nil {
		return nil, "", fmt.Errorf("无法获取邮件内容")
	}

	// 确保关闭reader
	_, err = io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("读取邮件内容失败: %w", err)
	}

	// 递归查找附件部分及其MIME类型
	var findAttachment func(bs *imap.BodyStructure, parentPath []int) ([]int, string)
	findAttachment = func(bs *imap.BodyStructure, parentPath []int) ([]int, string) {
		// 检查该部分是否为所需附件
		if bs.Disposition == "attachment" {
			attachmentFilename := bs.DispositionParams["filename"]
			if attachmentFilename == "" {
				attachmentFilename = bs.Params["name"]
			}
			if attachmentFilename == filename {
				return parentPath, bs.MIMEType + "/" + bs.MIMESubType
			}
		}

		// 如果是多部分邮件，检查所有子部分
		if bs.MIMEType == "multipart" {
			for i, part := range bs.Parts {
				// 构建子部分路径
				path := append(append([]int{}, parentPath...), i+1)
				if resultPath, mimeType := findAttachment(part, path); resultPath != nil {
					return resultPath, mimeType
				}
			}
		}
		return nil, ""
	}

	// 查找附件信息
	attachmentPath, attachmentMimeType := findAttachment(msg.BodyStructure, []int{})

	// 如果找到附件，尝试直接获取其内容
	if attachmentPath != nil && len(attachmentPath) > 0 {
		// 尝试通过特定路径获取附件内容
		attachmentSection := &imap.BodySectionName{
			Peek: true,
		}

		// 再次获取指定附件
		attachItems := []imap.FetchItem{attachmentSection.FetchItem()}
		attachMessages := make(chan *imap.Message, 1)
		attachDone := make(chan error, 1)

		go func() {
			attachDone <- c.Fetch(seqSet, attachItems, attachMessages)
		}()

		attachMsg := <-attachMessages
		if attachError := <-attachDone; attachError != nil {
			return nil, "", fmt.Errorf("获取附件内容失败: %w", attachError)
		}

		// 读取附件内容
		if attachmentContent := attachMsg.GetBody(attachmentSection); attachmentContent != nil {
			data, err := io.ReadAll(attachmentContent)
			if err != nil {
				return nil, "", fmt.Errorf("读取附件内容失败: %w", err)
			}
			return data, attachmentMimeType, nil
		}
	}

	// 如果无法通过常规方式获取附件，返回一个占位内容
	finalMimeType := "application/octet-stream"
	if attachmentMimeType != "" {
		finalMimeType = attachmentMimeType
	}

	return []byte("此附件内容无法解析。建议使用邮件客户端查看原始邮件。"), finalMimeType, nil
}

// SendEmail 发送邮件
func (m *MailClient) SendEmail(toAddress, subject, body, contentType string) error {
	// 使用smtp包连接服务器
	auth := smtp.PlainAuth("", m.EmailAddress, m.Password, m.SMTPServer)

	// 设置标头
	header := make(map[string]string)
	header["From"] = m.EmailAddress
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
	smtpAddr := fmt.Sprintf("%s:%d", m.SMTPServer, m.SMTPPort)

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
		config := &tls.Config{ServerName: m.SMTPServer}
		if err = c.StartTLS(config); err != nil {
			return fmt.Errorf("StartTLS失败: %w", err)
		}
	}

	// 进行身份验证
	if err = c.Auth(auth); err != nil {
		return fmt.Errorf("SMTP认证失败: %w", err)
	}

	// 设置发件人和收件人
	if err = c.Mail(m.EmailAddress); err != nil {
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
			addrList = append(addrList, fmt.Sprintf("%s <%s@%s>", addr.PersonalName, addr.MailboxName, addr.HostName))
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
