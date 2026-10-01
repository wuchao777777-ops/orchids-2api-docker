// Package errors 提供统一的错误处理机制
package errors

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"encoding/json"
)

// AppError 表示应用层错误，包含错误码、消息和可选的原因
type AppError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
	// RetryAfter, when positive, is published as the Retry-After header. A
	// capacity answer that took a minute of upstream retries to produce has to
	// tell the caller when to come back: without it the client only learns that
	// something failed and retries on its own schedule, which adds load to the
	// condition that produced the answer.
	RetryAfter time.Duration `json:"-"`
}

// ToJSON 返回错误的 JSON 表示
func (e *AppError) ToJSON() []byte {
	data, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"type":    e.Code,
			"message": e.Message,
		},
	})
	return data
}

// WriteResponse 将错误写入 HTTP 响应
func (e *AppError) WriteResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if e.RetryAfter > 0 {
		seconds := int(math.Ceil(e.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	w.WriteHeader(e.HTTPStatus)
	w.Write(e.ToJSON())
}

// New 创建新的应用错误
func New(code, message string, httpStatus int) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	}
}

// NewWithRetryAfter is New plus the upstream's own "come back in" hint, which
// WriteResponse publishes as the Retry-After header.
func NewWithRetryAfter(code, message string, httpStatus int, retryAfter time.Duration) *AppError {
	err := New(code, message, httpStatus)
	if retryAfter > 0 {
		err.RetryAfter = retryAfter
	}
	return err
}
