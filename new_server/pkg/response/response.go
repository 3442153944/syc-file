// Package response 统一业务接口的响应结构及分页格式。
package response

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// Envelope 保证成功、失败响应始终含有 code、message、data 三个字段。
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// TransformKey 用于认证后的按用户路径脱敏，不改变统一响应信封。
const TransformKey = "response.data_transform"

func Write(c *gin.Context, code int, message string, data any) {
	WriteHTTP(c, http.StatusOK, code, message, data)
}
func WriteHTTP(c *gin.Context, status, code int, message string, data any) {
	if transform, ok := c.Get(TransformKey); ok {
		if fn, ok := transform.(func(any) any); ok {
			data = fn(data)
			if msg, ok := fn(message).(string); ok {
				message = msg
			}
		}
	}
	c.JSON(status, Envelope{Code: code, Message: message, Data: data})
}
func OK(c *gin.Context, data any)                   { Write(c, 200, "ok", data) }
func Fail(c *gin.Context, code int, message string) { Write(c, code, message, nil) }

type PageData struct {
	List     any   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

func Page(c *gin.Context, list any, total int64, page, pageSize int) {
	OK(c, PageData{List: list, Total: total, Page: page, PageSize: pageSize})
}
