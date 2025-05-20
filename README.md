# Go邮件服务

基于Go和Gin框架实现的邮件服务，支持查看邮件列表、内容和附件的Web API。

## 功能

- 获取邮件列表
- 查看邮件内容
- 列出邮件附件
- 下载附件
- 发送邮件

## 运行环境要求

- Go 1.18 或更高版本

## 安装依赖

```bash
go mod tidy
```

## 配置

通过config/config.yaml文件配置邮箱信息:

```yaml
# 邮箱配置
email:
  imap_server: imap.ipage.com
  smtp_server: smtp.ipage.com
  email_address: aiteam@primeagencygroup.com
  password: REDACTED
  imap_port: 993
  smtp_port: 587
  use_ssl: true

# 服务器配置
server:
  port: 8080
  host: 0.0.0.0
```

配置文件查找顺序：
1. 当前工作目录下的 config/config.yaml
2. 可执行文件目录下的 config/config.yaml
3. 用户主目录下的 .go_email/config.yaml

## 启动服务

```bash
go run main.go
```

服务默认运行在8080端口，可在配置文件中修改。

## API 接口

### 获取邮件列表

```
GET /api/v1/emails/list?folder=INBOX&limit=10
```

### 获取邮件内容

```
GET /api/v1/emails/content/:uid?folder=INBOX
```

### 列出邮件附件

```
GET /api/v1/emails/attachments/:uid?folder=INBOX
```

### 下载附件

```
GET /api/v1/emails/download/:uid?filename=example.pdf&folder=INBOX
```

### 发送邮件

```
POST /api/v1/emails/send
```

请求体:

```json
{
  "to": "recipient@example.com",
  "subject": "测试邮件",
  "body": "这是一封测试邮件",
  "content_type": "text"  // 可选值: "text" 或 "html"
}
```

## 注意事项

- 确保邮箱服务器允许IMAP和SMTP访问
- 确保提供的邮箱账号和密码正确
- 某些邮箱服务可能需要app密码而不是常规密码 