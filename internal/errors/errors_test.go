package errors

import (
	"net/http"
	"net/http/httptest"
	"orchids-api/internal/testutil"
	"testing"
)

func TestAppError_ToJSON(t *testing.T) {
	err := New("invalid_request_error", "请求格式无效", http.StatusBadRequest)
	json := string(err.ToJSON())

	testutil.CheckNotEqual(t, json, "")
	testutil.CheckContain(t, json, `"type":"error"`)
	testutil.CheckContain(t, json, `"type":"invalid_request_error"`)
}

func TestAppError_WriteResponse(t *testing.T) {
	err := New("invalid_request_error", "请求格式无效", http.StatusBadRequest)
	w := httptest.NewRecorder()

	err.WriteResponse(w)

	testutil.CheckEqual(t, w.Code, http.StatusBadRequest)
	testutil.CheckEqual(t, w.Header().Get("Content-Type"), "application/json")
}

func TestNew(t *testing.T) {
	err := New("custom_code", "custom message", http.StatusTeapot)

	testutil.CheckEqual(t, err.Code, "custom_code")
	testutil.CheckEqual(t, err.Message, "custom message")
	testutil.CheckEqual(t, err.HTTPStatus, http.StatusTeapot)
}
