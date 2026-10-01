package handler

import (
	"bytes"
	"net/http"
	"orchids-api/internal/testutil"
	"testing"
)

func TestComputeRequestHash_ChangesWithAuthPathBody(t *testing.T) {
	h := &Handler{}
	mkReq := func(path, auth string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "http://example.com"+path, bytes.NewReader([]byte("{}")))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		return r
	}
	bodyA := []byte(`{"a":1}`)
	bodyB := []byte(`{"a":2}`)

	h1 := h.computeRequestHash(mkReq("/v1/messages", "Bearer x"), bodyA)
	h2 := h.computeRequestHash(mkReq("/v1/messages", "Bearer x"), bodyA)
	testutil.Equal(t, h1, h2)

	testutil.NotEqual(t, h1, h.computeRequestHash(mkReq("/v1/messages", "Bearer y"), bodyA))
	testutil.NotEqual(t, h1, h.computeRequestHash(mkReq("/v1/other", "Bearer x"), bodyA))
	testutil.NotEqual(t, h1, h.computeRequestHash(mkReq("/v1/messages", "Bearer x"), bodyB))
}
