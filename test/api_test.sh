#!/bin/bash

# 测试API脚本
BASE_URL="http://localhost:8080/api/v1/emails"

# 检查服务是否运行
echo "检查服务是否运行..."
curl -s "$BASE_URL/list?limit=1" > /dev/null
if [ $? -ne 0 ]; then
    echo "服务未运行或无法访问"
    exit 1
fi
echo "服务正在运行"

# 测试获取邮件列表
echo -e "\n1. 测试获取邮件列表"
curl -s "$BASE_URL/list?limit=3" | jq '.'

# 如果有邮件，获取第一封邮件的UID用于后续测试
UID=$(curl -s "$BASE_URL/list?limit=1" | jq '.[0].uid')
if [ -z "$UID" ] || [ "$UID" == "null" ]; then
    echo "未找到邮件，无法进行后续测试"
    exit 1
fi

echo -e "\n2. 测试获取邮件内容 (UID: $UID)"
curl -s "$BASE_URL/content/$UID" | jq '.'

echo -e "\n3. 测试获取邮件附件列表 (UID: $UID)"
curl -s "$BASE_URL/attachments/$UID" | jq '.'

# 测试发送邮件
echo -e "\n4. 测试发送邮件"
curl -s -X POST "$BASE_URL/send" \
  -H "Content-Type: application/json" \
  -d '{
    "to": "test@example.com",
    "subject": "测试邮件",
    "body": "这是一封测试邮件，由Go邮件服务发送。",
    "content_type": "text"
  }' | jq '.'

echo -e "\n测试完成" 