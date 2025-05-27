package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zxmrlc/log"
)

// 自定义Writer来捕获响应内容
type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

// 重写Write方法以捕获响应体
func (r responseBodyWriter) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// Logger is a middleware function that logs the each request.
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		start := time.Now()

		// 获取请求体
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
			// 因为读取了body，需要重新设置以便后续处理
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// 创建自定义ResponseWriter来捕获响应内容
		w := &responseBodyWriter{
			ResponseWriter: c.Writer,
			body:           bytes.NewBufferString(""),
		}
		c.Writer = w

		// 处理请求
		c.Next()

		// 结束时间
		end := time.Now()
		// 执行时间
		latency := end.Sub(start)

		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		path := c.Request.URL.Path
		userAgent := c.Request.UserAgent()

		// 获取并格式化响应体
		responseBody := w.body.String()

		// 尝试美化JSON响应
		var formattedResponse string
		var respObj interface{}
		if json.Unmarshal([]byte(responseBody), &respObj) == nil {
			// 成功解析为JSON
			formattedResponse = fmt.Sprintf("%+v", respObj)
			if len(formattedResponse) > 1000 {
				formattedResponse = formattedResponse[:1000] + "... (截断)"
			}
		} else {
			// 不是JSON或无法解析
			if len(responseBody) > 1000 {
				formattedResponse = responseBody[:1000] + "... (截断)"
			} else {
				formattedResponse = responseBody
			}
		}

		// 格式化请求体
		var formattedRequest string
		if len(requestBody) > 0 {
			var reqObj interface{}
			if json.Unmarshal(requestBody, &reqObj) == nil {
				// 成功解析为JSON
				formattedRequest = fmt.Sprintf("%+v", reqObj)
				if len(formattedRequest) > 500 {
					formattedRequest = formattedRequest[:500] + "... (截断)"
				}
			} else {
				// 不是JSON或无法解析
				if len(requestBody) > 500 {
					formattedRequest = string(requestBody[:500]) + "... (截断)"
				} else {
					formattedRequest = string(requestBody)
				}
			}
		} else {
			formattedRequest = "无请求体"
		}

		// 使用log包记录请求信息
		log.Infof("| %3d | %13v | %15s | %s | %s | %s |\n请求: %s\n响应: %s",
			statusCode,
			latency,
			clientIP,
			method,
			path,
			userAgent,
			formattedRequest,
			formattedResponse,
		)

		// 同时在控制台打印日志
		fmt.Printf("| %3d | %13v | %15s | %s | %s | %s |\n请求: %s\n响应: %s\n",
			statusCode,
			latency,
			clientIP,
			method,
			path,
			userAgent,
			formattedRequest,
			formattedResponse,
		)
	}
}
