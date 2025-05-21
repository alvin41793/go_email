package oss

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
)

// OSS响应结构
type UploadResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		FileURL     string `json:"fileUrl"`
		AbsoluteURL string `json:"absoluteUrl,omitempty"`
		FileName    string `json:"fileName,omitempty"`
		FileSize    int64  `json:"fileSize,omitempty"`
		FileType    string `json:"fileType,omitempty"`
	} `json:"data"`
}

type TokenResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		AccessToken string `json:"accessToken"`
	} `json:"data"`
}

func getToken() (string, error) {
	payload := map[string]interface{}{
		"client_id":     "ff80808195b14b9c0195b14b9cab0000",
		"client_secret": "edgk375852v9c2550s83bpr575kdf3p7",
		"validity_time": 4 * 60 * 60 * 1000,
	}

	body, _ := json.Marshal(payload)
	fmt.Printf("正在获取令牌...\n")

	resp, err := http.Post(
		"https://openapi.geekyum.com/channel/outer/link/getToken",
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", fmt.Errorf("获取令牌HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取令牌响应失败: %w", err)
	}

	fmt.Printf("收到令牌响应，状态码: %d，响应内容: %s\n", resp.StatusCode, string(respBody))

	var result TokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析令牌响应失败: %w, 响应内容: %s", err, string(respBody))
	}

	if result.Code != 0 && result.Code != 200 {
		return "", fmt.Errorf("获取令牌失败，错误码: %d, 错误信息: %s", result.Code, result.Message)
	}

	if result.Data.AccessToken == "" {
		return "", fmt.Errorf("获取令牌成功但未返回令牌内容，响应内容: %s", string(respBody))
	}

	return result.Data.AccessToken, nil
}

// UploadBase64ToOSS 将base64编码的数据上传到OSS
func UploadBase64ToOSS(filename string, base64Data string, fileType string) (string, error) {
	//fmt.Printf("准备上传base64数据，文件名: %s，类型: %s\n", filename, fileType)

	// 解码base64数据
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("解码base64数据失败: %w", err)
	}

	if len(data) == 0 {
		return "", fmt.Errorf("解码后的文件内容为空")
	}

	fmt.Printf("成功解码base64数据，大小: %d 字节\n", len(data))

	token, err := getToken()
	if err != nil {
		return "", fmt.Errorf("获取令牌失败: %w", err)
	}
	fmt.Printf("成功获取令牌: %s\n", token)

	// 如果没有提供文件类型，尝试从文件名获取
	if fileType == "" {
		ext := filepath.Ext(filename)
		if ext != "" {
			fileType = ext[1:] // 移除前导点号
		} else {
			fileType = "bin" // 默认二进制类型
		}
	}

	payload := map[string]interface{}{
		"header": map[string]string{"accessToken": token},
		"model": map[string]interface{}{
			"fileName":  filename,
			"fileBytes": base64Data, // 直接使用提供的base64数据
			"fileType":  fileType,
		},
	}

	body, _ := json.Marshal(payload)
	//fmt.Printf("准备发送请求，数据大小: %d 字节\n", len(body))

	resp, err := http.Post(
		"https://gateway.geekyum.com/service/recognize/upload",
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", fmt.Errorf("发送HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应内容失败: %w", err)
	}

	//fmt.Printf("收到响应，状态码: %d，响应内容: %s\n", resp.StatusCode, string(respBody))

	var result UploadResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w, 响应内容: %s", err, string(respBody))
	}

	if result.Code != 0 && result.Code != 200 {
		return "", fmt.Errorf("上传失败，错误码: %d, 错误信息: %s", result.Code, result.Message)
	}

	if result.Data.FileURL == "" {
		return "", fmt.Errorf("上传成功但未返回文件URL，响应内容: %s", string(respBody))
	}

	//fmt.Printf("文件上传成功，URL: %s\n", result.Data.FileURL)
	return result.Data.FileURL, nil
}
