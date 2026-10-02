package grok

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
)

// TestLocalErrorKeepsItsStatusAndMessage is the guard for the classification rule.
//
// writeGrokUpstreamError decides between "an upstream failed" and "the caller sent
// something we reject" by inspecting the error text. The status probe used to match
// a bare "status=", so any local error that happened to mention a status was
// reclassified as an upstream failure: it answered 5xx for a 4xx condition and
// replaced the local message with a generic sentence.
func TestLocalErrorKeepsItsStatusAndMessage(t *testing.T) {
	local := []string{
		"job status=404 not found in store",
		"account status=402 parked",
		"invalid request: expected status=200 payload",
		"store=true requires stream=false for this provider",
		"aspect_ratio is not supported",
	}
	for _, message := range local {
		rec := httptest.NewRecorder()
		writeGrokUpstreamError(rec, errors.New(message))
		testutil.CheckEqual(t, rec.Code, http.StatusBadRequest)
		testutil.CheckContain(t, rec.Body.String(), message)
	}
}

// TestUpstreamErrorIsStillSanitized pins the other half: a genuine upstream failure
// is still classified as one, answered 5xx, and stripped of upstream detail.
func TestUpstreamErrorIsStillSanitized(t *testing.T) {
	upstream := []struct {
		name        string
		message     string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "legacy 502",
			message:     `grok upstream status=502 body={"message":"bad gateway from xai","team":"classify-private-team","token":"classify-secret-token"}`,
			wantStatus:  http.StatusBadGateway,
			wantCode:    "server",
			wantMessage: "The upstream service is temporarily unavailable. Retry later.",
		},
		{
			name:        "legacy 403",
			message:     `grok cli upstream status=403 body={"message":"forbidden","team":"classify-private-team","token":"classify-secret-token"}`,
			wantStatus:  http.StatusServiceUnavailable,
			wantCode:    "auth_blocked",
			wantMessage: "The upstream account is not allowed to use this feature. Check its plan and permissions.",
		},
	}
	for _, tc := range upstream {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeGrokUpstreamError(rec, errors.New(tc.message))
			// Credentials belong to the operator-owned pool, so legacy 403
			// maps to 503 while a server-class upstream 502 stays 502.
			testutil.Equal(t, rec.Code, tc.wantStatus)
			got := rec.Header().Get("Content-Type")
			testutil.Falsef(t, !strings.HasPrefix(got, "application/json"), "Content-Type = %q, want application/json", got)
			var payload struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			err := json.Unmarshal(rec.Body.Bytes(), &payload)
			testutil.CheckNoError(t, err)
			testutil.Equal(t, payload.Error.Code, tc.wantCode)
			testutil.Equal(t, payload.Error.Type, "server_error")
			testutil.Equal(t, payload.Error.Message, tc.wantMessage)
			for _, leak := range []string{"body=", "status=", "classify-private-team", "classify-secret-token", "bad gateway from xai", "forbidden"} {
				testutil.CheckNotContain(t, rec.Body.String(), leak)
			}
		})
	}
}
