package utils

import (
	"log"
	"strings"
	"unicode/utf8"
)

// SanitizeUTF8 清理字符串中的非法UTF-8字符
func SanitizeUTF8(input string) string {
	if input == "" {
		return input
	}

	if utf8.ValidString(input) {
		return input
	}

	log.Printf("[字符集处理] 检测到非法UTF-8字符，进行清洗")

	// 将非法UTF-8字符替换为空格
	result := strings.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return ' '
		}
		return r
	}, input)

	// 如果仍然有非法字符，使用更激进的方式：只保留ASCII字符
	if !utf8.ValidString(result) {
		log.Printf("[字符集处理] 第一次清洗后仍有非法字符，只保留ASCII字符")
		result = strings.Map(func(r rune) rune {
			if r <= 127 {
				return r
			}
			return ' '
		}, input)
	}

	// 如果结果仍然有非法字符，最后的尝试：将所有不可见的非ASCII字符去除
	if !utf8.ValidString(result) {
		log.Printf("[字符集处理] 第二次清洗后仍有非法字符，只保留可见ASCII字符")
		var cleanResult strings.Builder
		for i := 0; i < len(input); i++ {
			// 只保留可见ASCII字符和空格
			if input[i] >= 32 && input[i] <= 126 || input[i] == ' ' || input[i] == '\n' || input[i] == '\t' {
				cleanResult.WriteByte(input[i])
			}
		}
		result = cleanResult.String()
	}

	return result
}
