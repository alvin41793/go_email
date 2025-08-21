package oss

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"path"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/spf13/viper"
)

// OSSConfig OSS配置结构
type OSSConfig struct {
	Endpoint        string
	AccessKeyID     string
	AccessKeySecret string
	BucketName      string
	Domain          string
}

// GetOSSConfig 从配置文件获取OSS配置
func GetOSSConfig() *OSSConfig {
	return &OSSConfig{
		Endpoint:        viper.GetString("aliyun.oss.endpoint"),
		access_key_id: REDACTED.GetString("aliyun.oss.access-key-id"),
		access_key_secret: REDACTED.GetString("aliyun.oss.access-key-secret"),
		BucketName:      viper.GetString("aliyun.oss.bucket-name"),
		Domain:          viper.GetString("aliyun.oss.domain"),
	}
}

// OSSUploader OSS上传器
type OSSUploader struct {
	config *OSSConfig
	client *oss.Client
	bucket *oss.Bucket
}

// NewOSSUploader 创建新的OSS上传器
func NewOSSUploader() (*OSSUploader, error) {
	config := GetOSSConfig()

	// 创建OSS客户端
	client, err := oss.New(config.Endpoint, config.AccessKeyID, config.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建OSS客户端失败: %v", err)
	}

	// 获取存储空间
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		return nil, fmt.Errorf("获取OSS存储空间失败: %v", err)
	}

	return &OSSUploader{
		config: config,
		client: client,
		bucket: bucket,
	}, nil
}

// UploadFileFromMultipart 从multipart文件上传到OSS
func (u *OSSUploader) UploadFileFromMultipart(file *multipart.FileHeader, folder string) (string, string, error) {
	// 打开文件
	src, err := file.Open()
	if err != nil {
		return "", "", fmt.Errorf("打开文件失败: %v", err)
	}
	defer src.Close()

	// 生成文件路径
	fileName := generateFileName(file.Filename)
	objectKey := path.Join(folder, fileName)

	// 上传文件
	err = u.bucket.PutObject(objectKey, src)
	if err != nil {
		return "", "", fmt.Errorf("上传文件到OSS失败: %v", err)
	}

	// 返回文件URL
	fileURL := fmt.Sprintf("%s/%s", u.config.Domain, objectKey)
	return fileURL, objectKey, nil
}

// UploadFile 从io.Reader上传文件到OSS
func (u *OSSUploader) UploadFile(reader io.Reader, fileName string, folder string) (string, string, error) {
	// 生成文件路径
	newFileName := generateFileName(fileName)
	objectKey := path.Join(folder, newFileName)

	// 上传文件
	err := u.bucket.PutObject(objectKey, reader)
	if err != nil {
		return "", "", fmt.Errorf("上传文件到OSS失败: %v", err)
	}

	// 返回文件URL
	fileURL := fmt.Sprintf("%s/%s", u.config.Domain, objectKey)
	return fileURL, objectKey, nil
}

// DeleteFile 删除OSS中的文件
func (u *OSSUploader) DeleteFile(objectKey string) error {
	err := u.bucket.DeleteObject(objectKey)
	if err != nil {
		return fmt.Errorf("删除OSS文件失败: %v", err)
	}
	return nil
}

// generateFileName 生成带时间戳的文件名
func generateFileName(originalName string) string {
	ext := path.Ext(originalName)
	name := originalName[:len(originalName)-len(ext)]
	timestamp := time.Now().Format("20060102150405")
	return fmt.Sprintf("%s_%s%s", name, timestamp, ext)
}

// IsFileExist 检查OSS中文件是否存在
func (u *OSSUploader) IsFileExist(objectKey string) (bool, error) {
	exist, err := u.bucket.IsObjectExist(objectKey)
	if err != nil {
		return false, fmt.Errorf("检查文件是否存在失败: %v", err)
	}
	return exist, nil
}

// UploadFileFromBase64 从base64编码的数据上传文件到OSS
func (u *OSSUploader) UploadFileFromBase64(base64Data, fileName, folder string) (string, string, error) {
	// 处理base64数据，移除可能的前缀（如 "data:image/jpeg;base64,"）
	if strings.Contains(base64Data, ",") {
		parts := strings.Split(base64Data, ",")
		if len(parts) > 1 {
			base64Data = parts[1]
		}
	}

	// 解码base64数据
	fileData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", "", fmt.Errorf("base64解码失败: %v", err)
	}

	// 生成唯一的文件名
	uniqueFileName := generateFileName(fileName)

	// 构建对象键
	var objectKey string
	if folder != "" {
		objectKey = fmt.Sprintf("%s/%s", folder, uniqueFileName)
	} else {
		objectKey = uniqueFileName
	}

	// 创建字节读取器
	reader := bytes.NewReader(fileData)

	// 上传文件到OSS
	err = u.bucket.PutObject(objectKey, reader)
	if err != nil {
		return "", "", fmt.Errorf("上传文件到OSS失败: %v", err)
	}

	// 构建文件URL
	fileURL := fmt.Sprintf("%s/%s", u.config.Domain, objectKey)

	return fileURL, objectKey, nil
}
