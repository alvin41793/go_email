package oss

import (
	"encoding/base64"
	"fmt"
	"os"
	"testing"

	"go_email/config"
)

// TestUploadLocalFileToOSS 读取 config/release.yaml 配置，
// 通过环境变量指定本地文件路径与上传目录，上传到阿里云 OSS 并打印返回 URL。
// 使用方法：
//
//	OSS_TEST_FILE=/absolute/path/to/file.pdf OSS_TEST_FOLDER=email_attachments go test -v ./pkg/utils/oss -run TestUploadLocalFileToOSS
//
// 其中 OSS_TEST_FILE 必填，OSS_TEST_FOLDER 可选（默认空字符串，不加子目录）。
func TestUploadLocalFileToOSS(t *testing.T) {
	// 初始化配置（读取 config/release.yaml）
	if err := os.Chdir("/Users/zhuyawen/Downloads/go_email"); err != nil {
		t.Fatalf("切换到项目根目录失败: %v", err)
	}
	if err := config.Init("release"); err != nil {
		t.Fatalf("初始化配置失败: %v", err)
	}

	filePath := "/Users/zhuyawen/Downloads/ARRIVAL NOTICE FOR BL# MEDUKV385258 on MSC VANDYA 544N.PDF"
	if filePath == "" {
		t.Skip("未设置 OSS_TEST_FILE 环境变量，跳过测试。示例：OSS_TEST_FILE=/path/to/file.pdf go test -v ./pkg/utils/oss -run TestUploadLocalFileToOSS")
	}
	folder := os.Getenv("OSS_TEST_FOLDER")

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("读取本地文件失败: %v", err)
	}

	base64Data := base64.StdEncoding.EncodeToString(data)
	fileName := "ARRIVAL NOTICE FOR #BL MEDUKV385258 on MSC VANDYA 544N2.PDF"

	uploader, err := NewOSSUploader()
	if err != nil {
		t.Fatalf("创建OSS上传器失败: %v", err)
	}

	url, objectKey, err := uploader.UploadFileFromBase64(base64Data, fileName, folder)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}

	t.Logf("上传成功\nURL: %s\nObjectKey: %s", url, objectKey)
	fmt.Printf("上传成功\nURL: %s\nObjectKey: %s\n", url, objectKey)
}
