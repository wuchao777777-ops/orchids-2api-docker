package util

import (
	"io"
	"net/http"
)

// DoReadBody executes a non-streaming control-plane request, reads at most limit
// bytes and closes the body. Partial bytes are intentionally retained on read
// errors: these providers historically decode them or classify the HTTP status.
// Only transport errors are returned; callers retain their own status policy.
func DoReadBody(client *http.Client, req *http.Request, limit int64) (*http.Response, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return resp, raw, nil
}
