package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 是API的标准响应结构
type Response struct {
	Code    int         `json:"code"`           // 状态码
	Message string      `json:"message"`        // 提示信息
	Data    interface{} `json:"data,omitempty"` // 数据
}

// 响应状态码
const (
	SUCCESS = 200 // 成功
	ERROR   = 500 // 错误
)

// ResponseOK 成功响应
func ResponseOK(c *gin.Context, data interface{}) {
	response := Response{
		Code:    SUCCESS,
		Message: "操作成功",
		Data:    data,
	}
	c.JSON(http.StatusOK, response)
}

// ResponseOKWithMsg 带消息的成功响应
func ResponseOKWithMsg(c *gin.Context, message string, data interface{}) {
	response := Response{
		Code:    SUCCESS,
		Message: message,
		Data:    data,
	}
	c.JSON(http.StatusOK, response)
}

// ResponseError 错误响应
func ResponseError(c *gin.Context, code int, message string) {
	response := Response{
		Code:    code,
		Message: message,
	}
	c.JSON(code, response)
}

// ResponseErrorWithData 带数据的错误响应
func ResponseErrorWithData(c *gin.Context, code int, message string, data interface{}) {
	response := Response{
		Code:    code,
		Message: message,
		Data:    data,
	}
	c.JSON(code, response)
}

// HandleError 处理错误的通用方法
func HandleError(c *gin.Context, err error) {
	ResponseError(c, http.StatusInternalServerError, err.Error())
}
