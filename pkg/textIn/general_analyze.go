package analyze_all

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-querystring/query"
)

type TextinOcr struct {
	AppID     string
	AppSecret string
	Host      string
}

type Options struct {
	PdfPwd            string `url:"pdf_pwd,omitempty"`
	Dpi               int    `url:"dpi,omitempty"`
	PageStart         int    `url:"page_start"`
	PageCount         int    `url:"page_count"`
	ApplyDocumentTree int    `url:"apply_document_tree,omitempty"`
	MarkdownDetails   int    `url:"markdown_details,omitempty"`
	TableFlavor       string `url:"table_flavor,omitempty"`
	GetImage          string `url:"get_image,omitempty"`
	ParseMode         string `url:"parse_mode,omitempty"`
	PageDetails       int    `url:"page_details,omitempty"`
}

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  struct {
		Markdown string `json:"markdown"`
	} `json:"result"`
}

func getFileContent(filePath string) ([]byte, error) {
	return os.ReadFile(filePath)
}

func (ocr *TextinOcr) recognizePDF2MD(image []byte, options Options, isUrl bool) (*http.Response, error) {
	url := ocr.Host + "/ai/service/v1/pdf_to_markdown"

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(image))
	if err != nil {
		return nil, err
	}

	req.Header.Set("x-ti-app-id", ocr.AppID)
	req.Header.Set("x-ti-secret-code", ocr.AppSecret)
	if isUrl {
		req.Header.Set("Content-Type", "text/plain")
	} else {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	q, _ := query.Values(options)
	req.URL.RawQuery = q.Encode()

	client := &http.Client{}
	return client.Do(req)
}

func writeFile(content, filePath string) error {
	return os.WriteFile(filePath, []byte(content), 0644)
}

// GeneralAnalyze 接收文件URL进行分析
func GeneralAnalyze(fileUrl string) (string, error) {
	textin := &TextinOcr{
		AppID:     "c67bd2b786bf256efe4bb7eb54643a62",
		AppSecret: "0768fda88657861bcced3510123cb011",
		Host:      "https://api.textin.com",
	}
	options := Options{
		PageStart:   0,
		PageCount:   1000, // 解析1000页
		TableFlavor: "md",
		ParseMode:   "scan", // 设置为scan模式
		Dpi:         144,    // 分辨率为144 dpi
		PageDetails: 0,      // 不包含页面细节信息
	}

	// 判断是使用文件还是URL
	if fileUrl == "" {
		return "", fmt.Errorf("文件URL不能为空")
	}

	fmt.Printf("使用URL分析文件: %s\n", fileUrl)

	// 验证URL格式
	if !strings.HasPrefix(fileUrl, "http://") && !strings.HasPrefix(fileUrl, "https://") {
		return "", fmt.Errorf("无效的URL格式，URL必须以http://或https://开头")
	}

	// 发起请求
	start := time.Now()
	fmt.Printf("开始发送请求...\n")
	resp, err := textin.recognizePDF2MD([]byte(fileUrl), options, true)
	if err != nil {
		return "", fmt.Errorf("请求文件分析失败: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("请求完成，耗时: %v，状态码: %d\n", time.Since(start), resp.StatusCode)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应内容失败: %w", err)
	}

	fmt.Printf("收到响应，响应内容长度: %d 字节\n", len(respBody))

	var jsonData Response
	if err := json.Unmarshal(respBody, &jsonData); err != nil {
		return "", fmt.Errorf("解析响应JSON失败: %w, 响应内容: %s", err, string(respBody))
	}

	// 检查响应状态码
	if jsonData.Code != 0 && jsonData.Code != 200 {
		return "", fmt.Errorf("API返回错误: 代码=%d, 消息=%s", jsonData.Code, jsonData.Message)
	}

	// 检查返回的Markdown内容
	if jsonData.Result.Markdown == "" {
		return "", fmt.Errorf("API返回成功但没有Markdown内容，响应体: %s", string(respBody))
	}

	fmt.Printf("成功获取Markdown内容，长度: %d 字节\n", len(jsonData.Result.Markdown))
	return jsonData.Result.Markdown, nil
}
