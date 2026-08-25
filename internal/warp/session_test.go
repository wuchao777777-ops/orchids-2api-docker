package warp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestSessionRefresh_UsesFirebaseWhenSuccessful(t *testing.T) {
	t.Parallel()

	sess := &session{refreshToken: "token-123"}
	var seen string
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seen = req.URL.String()
			if req.URL.String() != warpFirebaseURL {
				t.Fatalf("unexpected refresh URL: %s", req.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"id_token":"firebase-jwt","refresh_token":"rotated-token","expires_in":"3600"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	if err := sess.refresh(context.Background(), client); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	if seen != warpFirebaseURL {
		t.Fatalf("endpoint=%q want %q", seen, warpFirebaseURL)
	}
	if sess.currentJWT() != "firebase-jwt" {
		t.Fatalf("currentJWT=%q want firebase-jwt", sess.currentJWT())
	}
	if sess.currentRefreshToken() != "rotated-token" {
		t.Fatalf("currentRefreshToken=%q want rotated-token", sess.currentRefreshToken())
	}
}

func TestSessionRefresh_DoesNotFallbackToWarpTokenProxy(t *testing.T) {
	t.Parallel()

	sess := &session{refreshToken: "token-123"}
	var seen []string
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seen = append(seen, req.URL.String())
			switch req.URL.String() {
			case warpFirebaseURL:
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(bytes.NewBufferString(`{"error":"firebase unavailable"}`)),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected refresh URL: %s", req.URL.String())
				return nil, nil
			}
		}),
	}

	if err := sess.refresh(context.Background(), client); err == nil {
		t.Fatal("expected refresh error")
	}
	if len(seen) != 1 || seen[0] != warpFirebaseURL {
		t.Fatalf("seen refresh URLs=%v", seen)
	}
	if sess.currentJWT() != "" {
		t.Fatalf("currentJWT=%q want empty", sess.currentJWT())
	}
	if sess.currentRefreshToken() != "token-123" {
		t.Fatalf("currentRefreshToken=%q want token-123", sess.currentRefreshToken())
	}
}
