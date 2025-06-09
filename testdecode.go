package main

import (
	"fmt"
	"go_email/pkg/mailclient"
)

func Test2_decode() {
	// 测试不同的编码主题
	subjects := []string{
		"=?gb18030?B?sNfKq+L5?= <bsy@y-funglog.com>",
	}

	fmt.Println("MIME邮件主题解码测试")
	fmt.Println("=====================")

	for i, subject := range subjects {
		fmt.Printf("\n测试 %d:\n", i+1)
		fmt.Printf("原始编码: %s\n", subject)

		// 使用我们的解码函数
		decoded := mailclient.DecodeMIMESubject(subject)
		fmt.Printf("解码结果: %s\n", decoded)
	}

	fmt.Println("\n测试完成!")
}
