package warp

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestDoGraphQL_DecodesGzipResponse(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(`{"data":{"ok":true}}`)); err != nil {
		t.Fatalf("gzip response: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip response: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(compressed.Bytes())),
				Header:     http.Header{"Content-Encoding": []string{"gzip"}},
			}, nil
		}),
	}
	var response struct {
		Data struct {
			OK bool `json:"ok"`
		} `json:"data"`
	}
	if err := doGraphQL(context.Background(), client, warpGraphQLURL, "jwt", "Test", map[string]any{}, &response); err != nil {
		t.Fatalf("doGraphQL() error = %v", err)
	}
	if !response.Data.OK {
		t.Fatal("expected decoded gzip response")
	}
}

func TestFetchRequestLimitInfo_UsesOfficialWarpGraphQLHeaders(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.URL.String(); got != warpGraphQLV2URL+"?op=GetRequestLimitInfo" {
				t.Fatalf("unexpected graphql url: %s", got)
			}
			if got := req.Header.Get("X-Warp-Client-ID"); got != "warp-app" {
				t.Fatalf("unexpected client id header: %q", got)
			}
			if got := req.Header.Get("X-Warp-OS-Category"); got != warpOSCategory() {
				t.Fatalf("unexpected os category header: %q", got)
			}
			if got := req.Header.Get("User-Agent"); got != "" {
				t.Fatalf("unexpected user agent header: %q", got)
			}
			if got := req.Header.Get("Accept-Encoding"); got != "gzip" {
				t.Fatalf("unexpected accept encoding header: %q", got)
			}
			body := `{"data":{"user":{"__typename":"UserOutput","user":{"workspaces":[{"uid":"ws-1","bonusGrantsInfo":{"grants":[]}}],"requestLimitInfo":{"isUnlimited":false,"nextRefreshTime":"2026-03-15T00:00:00Z","requestLimit":100,"requestsUsedSinceLastRefresh":25,"requestLimitRefreshDuration":"MONTHLY"},"bonusGrants":[{"requestCreditsGranted":7,"requestCreditsRemaining":7,"expiration":"2026-03-20T00:00:00Z","reason":"bonus","userFacingMessage":"bonus"}]}}},"errors":[]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	info, bonuses, err := fetchRequestLimitInfo(context.Background(), client, "jwt")
	if err != nil {
		t.Fatalf("fetchRequestLimitInfo() error = %v", err)
	}
	if info == nil {
		t.Fatal("expected request limit info")
	}
	if info.RequestLimit != 100 || info.RequestsUsedSinceLastRefresh != 25 {
		t.Fatalf("unexpected limit info: %+v", info)
	}
	if len(bonuses) != 1 || bonuses[0].RequestCreditsRemaining != 7 {
		t.Fatalf("unexpected bonuses: %+v", bonuses)
	}
}

func TestRefundCredits_UsesV2EndpointAndExplicitRequestID(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.URL.String(); got != warpGraphQLV2URL+"?op=ProvideNegativeFeedbackResponseForAiConversation" {
				t.Fatalf("unexpected refund URL: %s", got)
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read refund request: %v", err)
			}
			if !strings.Contains(string(body), `"requestIds":["upstream-request-1"]`) || !strings.Contains(string(body), `"conversationId":"conversation-1"`) {
				t.Fatalf("refund request does not contain explicit upstream ID: %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":{"provideNegativeFeedbackResponseForAiConversation":{"__typename":"RequestsRefundedOutput","requestsRefunded":1}}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}

	if err := refundCredits(context.Background(), client, "jwt", "conversation-1", []string{"upstream-request-1"}); err != nil {
		t.Fatalf("refundCredits() error = %v", err)
	}
}

func TestRefundCredits_SupportsMultipleRequestIDs(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read refund request: %v", err)
		}
		if !strings.Contains(string(body), `"requestIds":["request-1","request-2"]`) {
			t.Fatalf("refund request IDs missing: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"provideNegativeFeedbackResponseForAiConversation":{"__typename":"RequestsRefundedOutput","requestsRefunded":2}}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
	if err := refundCredits(context.Background(), client, "jwt", "conversation-1", []string{"request-1", "request-2"}); err != nil {
		t.Fatalf("refundCredits() error = %v", err)
	}
	if got := normalizeRequestIDs([]string{" request-1 ", "request-1", "", "request-2"}); !slices.Equal(got, []string{"request-1", "request-2"}) {
		t.Fatalf("normalizeRequestIDs() = %v", got)
	}
}

func TestDoGraphQL_PreservesWarpErrorCode(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"limit"}`)),
			Header:     http.Header{"X-Warp-Error-Code": []string{"REQUEST_LIMIT_EXCEEDED"}},
		}, nil
	})}
	err := doGraphQL(context.Background(), client, warpGraphQLV2URL, "jwt", "Test", map[string]any{}, &struct{}{})
	if WarpErrorCode(err) != "REQUEST_LIMIT_EXCEEDED" || !strings.Contains(err.Error(), "REQUEST_LIMIT_EXCEEDED") {
		t.Fatalf("error code not preserved: %v", err)
	}
}

func TestFetchConversationUsage_FiltersConversationAndPreservesCostBreakdown(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != warpGraphQLV2URL+"?op=GetConversationUsage" {
			t.Fatalf("unexpected usage URL: %s", got)
		}
		body := `{"data":{"user":{"__typename":"UserOutput","user":{"conversationUsage":[{"conversationId":"other","lastUpdated":"x","usageMetadata":{}},{"conversationId":"conversation-1","lastUpdated":"2026-08-24T00:00:00Z","usageMetadata":{"contextWindowUsage":0.5,"creditsSpent":9.25,"platformCreditsSpent":1.5,"totalProviderCostInCents":3.75,"tokenUsage":[{"modelId":"m1","totalTokens":100},{"modelId":"m2","totalTokens":50}],"contextWindowSegments":[{"segmentType":"SYSTEM_PROMPT","tokenCount":20},{"segmentType":"TOOL_DEFINITIONS","tokenCount":30},{"segmentType":"CONVERSATION_HISTORY","tokenCount":40},{"segmentType":"LATEST_INPUT","tokenCount":10}]}}]}}}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}

	usage, err := fetchConversationUsage(context.Background(), client, "jwt", "conversation-1")
	if err != nil {
		t.Fatalf("fetchConversationUsage() error = %v", err)
	}
	if usage.CreditsSpent != 9.25 || usage.PlatformCreditsSpent != 1.5 || usage.TotalProviderCostInCents != 3.75 || usage.TotalTokens != 150 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if usage.SystemPromptTokens != 20 || usage.ToolDefinitionTokens != 30 || usage.ConversationHistoryTokens != 40 || usage.LatestInputTokens != 10 {
		t.Fatalf("unexpected segment usage: %+v", usage)
	}
}
