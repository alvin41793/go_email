package mailclient

import (
	"io"
	"net/mail"
	"strings"
	"testing"
)

func TestFindEmailBodyStart(t *testing.T) {
	// 测试查找邮件正文开始位置的功能
	rawEmail := `Received: from 127.0.0.1 (localhost [127.0.0.1])
	by mail.example.com (Postfix) with ESMTP id 12345
	for <user@example.com>; Mon, 1 Jan 2024 12:00:00 +0000
Return-Path: <sender@example.com>
X-Originating-Ip: [192.168.1.100]
From: sender@example.com
To: user@example.com
Subject: Test Single Part Email
Date: Mon, 1 Jan 2024 12:00:00 +0000
Content-Type: text/plain; charset=utf-8

This is the actual email body content.
It should not contain any email headers.
This is line 2 of the email body.`

	bodyStart := findEmailBodyStart(rawEmail)
	if bodyStart == -1 {
		t.Fatalf("无法找到邮件正文开始位置")
	}

	body := rawEmail[bodyStart:]
	cleanedBody := strings.TrimSpace(body)

	if !strings.Contains(cleanedBody, "This is the actual email body content.") {
		t.Errorf("邮件正文应该包含实际内容，实际内容: %s", cleanedBody)
	}

	// 验证邮件正文不包含邮件头部信息
	if strings.Contains(cleanedBody, "Received: from") {
		t.Errorf("邮件正文不应该包含 Received 头部信息")
	}

	if strings.Contains(cleanedBody, "Return-Path:") {
		t.Errorf("邮件正文不应该包含 Return-Path 头部信息")
	}

	if strings.Contains(cleanedBody, "X-Originating-Ip:") {
		t.Errorf("邮件正文不应该包含 X-Originating-Ip 头部信息")
	}

	if strings.Contains(cleanedBody, "Content-Type:") {
		t.Errorf("邮件正文不应该包含 Content-Type 头部信息")
	}

	t.Logf("找到邮件正文开始位置: %d", bodyStart)
	t.Logf("解析出的邮件正文: %s", cleanedBody)
}

func TestMailReadMessage(t *testing.T) {
	// 测试使用 mail.ReadMessage 解析邮件
	rawEmail := `From: sender@example.com
To: user@example.com
Subject: Test Email
Content-Type: text/plain; charset=utf-8

This is the email body content.
Second line of body.`

	reader := strings.NewReader(rawEmail)
	msg, err := mail.ReadMessage(reader)
	if err != nil {
		t.Fatalf("mail.ReadMessage 失败: %v", err)
	}

	// 验证头部解析
	if from := msg.Header.Get("From"); from != "sender@example.com" {
		t.Errorf("From 头部解析错误, 期望: sender@example.com, 实际: %s", from)
	}

	if to := msg.Header.Get("To"); to != "user@example.com" {
		t.Errorf("To 头部解析错误, 期望: user@example.com, 实际: %s", to)
	}

	if subject := msg.Header.Get("Subject"); subject != "Test Email" {
		t.Errorf("Subject 头部解析错误, 期望: Test Email, 实际: %s", subject)
	}

	// 读取正文
	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("读取邮件正文失败: %v", err)
	}

	body := string(bodyBytes)
	cleanedBody := strings.TrimSpace(body)

	if !strings.Contains(cleanedBody, "This is the email body content.") {
		t.Errorf("邮件正文应该包含实际内容，实际内容: %s", cleanedBody)
	}

	if strings.Contains(cleanedBody, "From:") || strings.Contains(cleanedBody, "To:") {
		t.Errorf("邮件正文不应该包含头部信息，实际内容: %s", cleanedBody)
	}

	t.Logf("解析结果:")
	t.Logf("From: %s", msg.Header.Get("From"))
	t.Logf("To: %s", msg.Header.Get("To"))
	t.Logf("Subject: %s", msg.Header.Get("Subject"))
	t.Logf("Body: %s", cleanedBody)
}

func TestIsEmailHeader(t *testing.T) {
	// 测试邮件头部识别功能
	testCases := []struct {
		line     string
		expected bool
	}{
		{"From: sender@example.com", true},
		{"To: user@example.com", true},
		{"Subject: Test Email", true},
		{"Date: Mon, 1 Jan 2024 12:00:00 +0000", true},
		{"Content-Type: text/plain", true},
		{"Received: from 127.0.0.1", true},
		{"Return-Path: <sender@example.com>", true},
		{"X-Originating-Ip: [192.168.1.100]", true},
		{"This is email body content", false},
		{"", false},
		{"Not a header line", false},
	}

	for _, tc := range testCases {
		result := isEmailHeader(tc.line)
		if result != tc.expected {
			t.Errorf("isEmailHeader(%q) = %v, 期望 %v", tc.line, result, tc.expected)
		}
	}
}

func TestComplexEmailParsing(t *testing.T) {
	// 测试复杂的邮件解析场景，包含您遇到的问题中的邮件头部
	complexEmail := `Received: from 127.0.0.1 (localhost [127.0.0.1])
	by mail.example.com (Postfix) with ESMTP id 12345ABC
	for <recipient@example.com>; Mon, 1 Jan 2024 12:00:00 +0000 (UTC)
Received: from smtp.sender.com (smtp.sender.com [203.0.113.1])
	by mail.example.com (Postfix) with ESMTPS id 67890DEF
	for <recipient@example.com>; Mon, 1 Jan 2024 11:59:58 +0000 (UTC)
Return-Path: <sender@example.com>
X-Originating-Ip: [192.168.1.100]
X-Spam-Checker-Version: SpamAssassin 3.4.6
X-Spam-Level: 
X-Spam-Status: No, score=-0.1 required=5.0
Message-ID: <abc123@sender.com>
From: "Sender Name" <sender@example.com>
To: "Recipient Name" <recipient@example.com>
Subject: Important: Shipping Update - Container MSKU1234567
Date: Mon, 1 Jan 2024 11:59:55 +0000
MIME-Version: 1.0
Content-Type: text/plain; charset=UTF-8
Content-Transfer-Encoding: 7bit

Dear Recipient,

This is the actual email content about shipping updates.
Container: MSKU1234567
Status: In Transit
Expected Arrival: 2024-01-15

Please find the details below:
- HBL Number: HBL789012345
- Vessel: EVER GIVEN
- Port: Shanghai

Best regards,
Shipping Team`

	// 测试 findEmailBodyStart 函数
	bodyStart := findEmailBodyStart(complexEmail)
	if bodyStart == -1 {
		t.Fatalf("无法找到复杂邮件的正文开始位置")
	}

	body := complexEmail[bodyStart:]
	cleanedBody := strings.TrimSpace(body)

	// 验证邮件正文包含实际内容
	if !strings.Contains(cleanedBody, "Dear Recipient") {
		t.Errorf("邮件正文应该包含 'Dear Recipient'")
	}

	if !strings.Contains(cleanedBody, "Container: MSKU1234567") {
		t.Errorf("邮件正文应该包含容器信息")
	}

	if !strings.Contains(cleanedBody, "HBL Number: HBL789012345") {
		t.Errorf("邮件正文应该包含HBL号码")
	}

	// 验证邮件正文不包含任何邮件头部信息
	headersToCheck := []string{
		"Received: from",
		"Return-Path:",
		"X-Originating-Ip:",
		"X-Spam-Checker-Version:",
		"Message-ID:",
		"MIME-Version:",
		"Content-Type:",
		"Content-Transfer-Encoding:",
	}

	for _, header := range headersToCheck {
		if strings.Contains(cleanedBody, header) {
			t.Errorf("邮件正文不应该包含头部信息: %s", header)
		}
	}

	t.Logf("复杂邮件解析成功")
	t.Logf("正文开始位置: %d", bodyStart)
	t.Logf("正文长度: %d", len(cleanedBody))
	t.Logf("正文前100字符: %s", cleanedBody[:min(100, len(cleanedBody))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
