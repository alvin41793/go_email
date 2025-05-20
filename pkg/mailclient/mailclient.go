package mailclient

import (
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/smtp"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
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
	Filename string  `json:"filename"`
	SizeKB   float64 `json:"size_kb"`
	MimeType string  `json:"mime_type"`
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
		bodyBytes, err := io.ReadAll(r)
		if err == nil {
			// 尝试将邮件解析为字符串
			bodyContent := string(bodyBytes)
			fmt.Println("bodyContent", bodyContent)
			// 检查邮件类型并提取内容

			fmt.Println("msg.BodyStructure.MIMEType==============================", msg.BodyStructure.MIMEType)

			if msg.BodyStructure.MIMEType == "text" {
				if msg.BodyStructure.MIMESubType == "plain" {
					email.Body = bodyContent
				} else if msg.BodyStructure.MIMESubType == "html" {
					email.BodyHTML = bodyContent
				}
			} else if msg.BodyStructure.MIMEType == "multipart" {
				// 对于多部分邮件，我们需要使用更复杂的解析逻辑
				// 这里采用简化方式：返回原始内容并添加标记

				// 由于原始数据可能包含二进制数据，需要处理
				textBody := ""
				htmlBody := ""

				fmt.Println("msg.BodyStructure.Parts=======================================", msg.BodyStructure.Parts)
				// 查找纯文本和HTML部分
				for _, part := range msg.BodyStructure.Parts {
					fmt.Println("part.MIMEType", part.MIMEType)
					if part.MIMEType == "text" {
						// 获取此部分的内容
						partSection := &imap.BodySectionName{
							Peek: true,
						}
						fmt.Println("part.MIMESubType", part.MIMESubType)
						// 根据part的位置创建section
						if part.MIMESubType == "plain" {
							partReader := msg.GetBody(partSection)
							fmt.Println("part.Body11", partReader)
							if partReader != nil {
								partBytes, _ := io.ReadAll(partReader)
								textBody = string(partBytes)
								fmt.Println("part.Body22", textBody)
							}
						} else if part.MIMESubType == "html" {
							partReader := msg.GetBody(partSection)
							fmt.Println("partReader11", partReader)

							if partReader != nil {
								partBytes, _ := io.ReadAll(partReader)
								htmlBody = string(partBytes)
								fmt.Println("part.BodyHTML", htmlBody)
							}
						}
					} else if part.Disposition == "attachment" {
						// 处理附件

						// 优先使用DispositionParams中的filename，如果没有则使用Params中的name
						attachmentFilename := part.DispositionParams["filename"]
						if attachmentFilename == "" {
							attachmentFilename = part.Params["name"]
						}

						email.Attachments = append(email.Attachments, AttachmentInfo{
							Filename: attachmentFilename,
							SizeKB:   float64(part.Size) / 1024.0,
							MimeType: part.MIMEType + "/" + part.MIMESubType,
						})
					}
				}

				// 如果还是无法解析文本内容，返回完整的邮件原始内容
				if textBody == "" {
					email.Body = "无法解析纯文本内容，邮件可能是复杂格式。原始内容大小: " + fmt.Sprintf("%d 字节", len(bodyBytes))
				} else {
					email.Body = textBody
				}

				if htmlBody == "" {
					email.BodyHTML = "无法解析HTML内容，邮件可能是复杂格式。"
				} else {
					email.BodyHTML = htmlBody
				}
			}

			// 如果Body和BodyHTML都为空，返回完整内容
			if email.Body == "" && email.BodyHTML == "" {
				email.Body = "邮件内容解析失败，返回原始内容:\n" + bodyContent
			}
		}
	}

	return email, nil
}

// GetAttachment 获取特定邮件的特定附件
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
	_, err = ioutil.ReadAll(reader)
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
			data, err := ioutil.ReadAll(attachmentContent)
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
