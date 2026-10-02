package grok

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OAuth endpoints share a bounded form transport, but keep their headers,
// decoding contracts and error classifiers separate. The limit intentionally
// truncates rather than rejecting oversized bodies, matching existing behavior.
func postOAuthForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, limit int64,
	prepare func(*http.Request), classify func([]byte, int) error) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if prepare != nil {
		prepare(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err == nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		err = classify(body, resp.StatusCode)
	}
	return body, resp.StatusCode, err
}
