package grok

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"encoding/json"

	"orchids-api/internal/middleware"
	"orchids-api/internal/testutil"
)

func TestResponsesChatPathMapsTheChannelPrefix(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"/workbuddy/v1/responses":         "/workbuddy/v1/chat/completions",
		"/workbuddy/v1/responses/compact": "/workbuddy/v1/chat/completions",
		"/cline/v1/responses/":            "/cline/v1/chat/completions",
		"/v1/responses":                   "/v1/chat/completions",
		"  /qoder/v1/responses  ":         "/qoder/v1/chat/completions",
		"/something/else":                 "/v1/chat/completions",
	}
	for path, want := range cases {
		testutil.Equal(t, responsesChatPath(path), want)
	}
}

type recordedChatCall struct {
	path string
	body map[string]interface{}
}

// recordingChat captures the inner chat request and replies with a complete
// chat-completions SSE stream, which is what the shared handler emits.
func recordingChat(t *testing.T, calls *[]recordedChatCall, mu *sync.Mutex) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		testutil.CheckNoError(t, err, "inner chat body: %v")
		var decoded map[string]interface{}
		_ = json.Unmarshal(raw, &decoded)
		mu.Lock()
		*calls = append(*calls, recordedChatCall{path: r.URL.Path, body: decoded})
		mu.Unlock()
		if streaming, _ := decoded["stream"].(bool); !streaming {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl-1","model":"gpt-5.6-luna","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, frame := range []string{
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, frame+"\n\n")
		}
	}
}

// Every event on the bridged stream carries a sequence_number that increases by
// one: a client that reconnects with Last-Event-ID asks for everything after the
// last number it saw, so an event without one cannot be ordered at all.
func TestResponsesBridgeStreamNumbersEveryEvent(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := []recordedChatCall{}
	bridge := ResponsesBridgeHandler(recordingChat(t, &calls, &mu), ResponsesBridgeOptions{})

	req := httptest.NewRequest(http.MethodPost, "/qoder/v1/responses",
		strings.NewReader(`{"model":"gpt-5.6-luna","input":"say hi","stream":true}`))
	rec := httptest.NewRecorder()
	bridge(rec, req)
	testutil.Equal(t, rec.Code, http.StatusOK)

	var numbers []int
	seen := 0
	if err := consumeCompatibleSSE(strings.NewReader(rec.Body.String()), func(event compatibleSSEEvent) error {
		// The [DONE] terminator is a chat-completions habit, not a Responses
		// event, so it carries no envelope and no number.
		if strings.TrimSpace(string(event.Data())) == "[DONE]" {
			return nil
		}
		seen++
		var payload map[string]interface{}
		if err := json.Unmarshal(event.Data(), &payload); err != nil {
			return fmt.Errorf("event %q: %v", event.Event, err)
		}
		number, ok := payload["sequence_number"].(float64)
		if !ok {
			return fmt.Errorf("event %q carries no sequence_number", event.Event)
		}
		numbers = append(numbers, int(number))
		return nil
	}); err != nil {
		t.Fatalf("consume bridged stream: %v", err)
	}
	testutil.Falsef(t, seen == 0, "the bridge produced no events")
	testutil.Equal(t, len(numbers), seen)
	// The first number is 0 and each following one is exactly one higher, so a
	// client can resume from any of them.
	for i, number := range numbers {
		testutil.Equal(t, number, i)
	}
}

func TestResponsesBridgeStreamsChatAsResponses(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := []recordedChatCall{}
	bridge := ResponsesBridgeHandler(recordingChat(t, &calls, &mu), ResponsesBridgeOptions{})

	req := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/responses",
		strings.NewReader(`{"model":"gpt-5.6-luna","instructions":"be brief","input":"say hi","stream":true}`))
	rec := httptest.NewRecorder()
	bridge(rec, req)

	testutil.Equal(t, rec.Code, http.StatusOK)
	out := rec.Body.String()
	for _, want := range []string{"event: response.created", "response.output_text.delta", `"hello"`, "event: response.completed"} {
		testutil.MustContain(t, out, want)
	}

	mu.Lock()
	defer mu.Unlock()
	testutil.Equal(t, len(calls), 1)
	testutil.Equal(t, calls[0].path, "/workbuddy/v1/chat/completions")
	messages, _ := calls[0].body["messages"].([]interface{})
	testutil.Equal(t, len(messages), 2)
	first, _ := messages[0].(map[string]interface{})
	testutil.Equal(t, first["role"], "system")
	testutil.Equal(t, first["content"], "be brief")
	if stream, _ := calls[0].body["stream"].(bool); !stream {
		t.Fatalf("inner stream = %#v, want true", calls[0].body["stream"])
	}
}

func TestResponsesBridgeNonStreamReturnsAResponseObject(t *testing.T) {
	t.Parallel()

	chat := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"chatcmpl-2","object":"chat.completion","created":1,"model":"gpt-5.6-luna","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}
	bridge := ResponsesBridgeHandler(chat, ResponsesBridgeOptions{})

	req := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/responses",
		strings.NewReader(`{"model":"gpt-5.6-luna","input":"say hi"}`))
	rec := httptest.NewRecorder()
	bridge(rec, req)

	testutil.Equal(t, rec.Code, http.StatusOK)
	var decoded map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &decoded)
	testutil.CheckNoError(t, err)
	testutil.Equal(t, decoded["object"], "response")
	testutil.Equal(t, decoded["model"], "gpt-5.6-luna")
	testutil.MustContain(t, rec.Body.String(), "hello")
}

func TestResponsesBridgeForwardsChatErrors(t *testing.T) {
	t.Parallel()

	chat := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"model not found","type":"invalid_request_error"}}`)
	}
	bridge := ResponsesBridgeHandler(chat, ResponsesBridgeOptions{})

	req := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/responses",
		strings.NewReader(`{"model":"does-not-exist","input":"hi","stream":true}`))
	rec := httptest.NewRecorder()
	bridge(rec, req)

	testutil.Equal(t, rec.Code, http.StatusBadRequest)
	testutil.MustContain(t, rec.Body.String(), "model not found")
}

func TestResponsesBridgeRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	bridge := ResponsesBridgeHandler(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner chat handler must not run for an invalid request")
	}, ResponsesBridgeOptions{})

	for name, body := range map[string]string{
		"missing_model": `{"input":"hi"}`,
		"missing_input": `{"model":"gpt-5.6-luna"}`,
		"broken_json":   `{"model":`,
		"background":    `{"model":"gpt-5.6-luna","input":"hi","background":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/responses", strings.NewReader(body))
			rec := httptest.NewRecorder()
			bridge(rec, req)
			testutil.Equal(t, rec.Code, http.StatusBadRequest)
		})
	}
}

func TestResponsesBridgeRejectsNonPost(t *testing.T) {
	t.Parallel()

	bridge := ResponsesBridgeHandler(func(w http.ResponseWriter, r *http.Request) {}, ResponsesBridgeOptions{})
	rec := httptest.NewRecorder()
	bridge(rec, httptest.NewRequest(http.MethodGet, "/workbuddy/v1/responses", nil))
	testutil.Equal(t, rec.Code, http.StatusMethodNotAllowed)
}

func TestResponsesChannelSubpathServesCompactAndTrailingSlash(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := []recordedChatCall{}
	handler := ResponsesChannelSubpath(recordingChat(t, &calls, &mu), ResponsesBridgeOptions{})

	for name, target := range map[string]string{
		"trailing_slash": "/qoder/v1/responses/",
		"compact":        "/workbuddy/v1/responses/compact",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"model":"gpt-5.6-luna","input":"summarise the thread","stream":true}`))
			rec := httptest.NewRecorder()
			handler(rec, req)
			testutil.Equal(t, rec.Code, http.StatusOK)
			testutil.MustContain(t, rec.Body.String(), "event: response.completed")
		})
	}

	mu.Lock()
	defer mu.Unlock()
	testutil.Equal(t, len(calls), 2)
	// The sub-tests run in map order, so compare the set of paths.
	paths := map[string]bool{}
	for _, call := range calls {
		paths[call.path] = true
	}
	testutil.Falsef(t, !paths["/qoder/v1/chat/completions"] || !paths["/workbuddy/v1/chat/completions"], "inner chat paths = %v, want the same channel prefix as the request", paths)
}

// The chat-only channels keep no response store, so /responses/{id} must answer
// with the Responses error envelope instead of Go's plain-text 404.
func TestResponsesChannelSubpathReportsUnstoredResponses(t *testing.T) {
	t.Parallel()

	handler := ResponsesChannelSubpath(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the create handler must not serve a resource path")
	}, ResponsesBridgeOptions{})

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/cline/v1/responses/resp_123", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		testutil.Equal(t, rec.Code, http.StatusNotFound)
		testutil.MustContain(t, rec.Body.String(), "response_not_found")
	}

	put := httptest.NewRequest(http.MethodPut, "/cline/v1/responses/resp_123", nil)
	rec := httptest.NewRecorder()
	handler(rec, put)
	testutil.Equal(t, rec.Code, http.StatusMethodNotAllowed)
	allow := rec.Header().Get("Allow")
	testutil.Falsef(t, !strings.Contains(allow, "GET") || !strings.Contains(allow, "DELETE"), "Allow = %q, want GET and DELETE", allow)
}

func TestResponsesBridgeStoresAndServesResponses(t *testing.T) {
	_, s, _ := setupValidationHandler(t)

	var mu sync.Mutex
	var chatBodies []map[string]interface{}
	chat := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var decoded map[string]interface{}
		_ = json.Unmarshal(raw, &decoded)
		mu.Lock()
		chatBodies = append(chatBodies, decoded)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"chatcmpl-9","object":"chat.completion","created":1,"model":"gpt-5.6-luna","choices":[{"index":0,"message":{"role":"assistant","content":"stored-answer"},"finish_reason":"stop"}]}`)
	}
	opts := ResponsesBridgeOptions{Store: s}
	bridge := ResponsesBridgeHandler(chat, opts)
	resource := ResponsesResourceHandler(opts)

	create := httptest.NewRecorder()
	bridge(create, httptest.NewRequest(http.MethodPost, "/workbuddy/v1/responses",
		strings.NewReader(`{"model":"gpt-5.6-luna","input":"hi","store":true}`)))
	testutil.Equal(t, create.Code, http.StatusOK)
	var created map[string]interface{}
	err := json.Unmarshal(create.Body.Bytes(), &created)
	testutil.CheckNoError(t, err)
	responseID, _ := created["id"].(string)
	testutil.Falsef(t, !strings.HasPrefix(responseID, "resp_"), "response id = %q, want a resp_ id", responseID)

	get := httptest.NewRecorder()
	resource(get, httptest.NewRequest(http.MethodGet, "/workbuddy/v1/responses/"+responseID, nil))
	testutil.Falsef(t, get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "stored-answer"), "get status=%d body=%s", get.Code, get.Body.String())

	// A continuation must replay the stored conversation upstream.
	continuation := httptest.NewRecorder()
	bridge(continuation, httptest.NewRequest(http.MethodPost, "/workbuddy/v1/responses",
		strings.NewReader(`{"model":"gpt-5.6-luna","input":"again","previous_response_id":"`+responseID+`"}`)))
	testutil.Equal(t, continuation.Code, http.StatusOK)
	mu.Lock()
	last := chatBodies[len(chatBodies)-1]
	mu.Unlock()
	testutil.MustContain(t, fmt.Sprint(last["messages"]), "stored-answer")

	deleted := httptest.NewRecorder()
	resource(deleted, httptest.NewRequest(http.MethodDelete, "/workbuddy/v1/responses/"+responseID, nil))
	testutil.Falsef(t, deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted":true`), "delete status=%d body=%s", deleted.Code, deleted.Body.String())
	gone := httptest.NewRecorder()
	resource(gone, httptest.NewRequest(http.MethodGet, "/workbuddy/v1/responses/"+responseID, nil))
	testutil.Equal(t, gone.Code, http.StatusNotFound)
}

func TestResponsesBridgeStoresStreamedResponse(t *testing.T) {
	_, s, _ := setupValidationHandler(t)

	var mu sync.Mutex
	calls := []recordedChatCall{}
	opts := ResponsesBridgeOptions{Store: s}
	bridge := ResponsesBridgeHandler(recordingChat(t, &calls, &mu), opts)
	resource := ResponsesResourceHandler(opts)

	stream := httptest.NewRecorder()
	bridge(stream, httptest.NewRequest(http.MethodPost, "/workbuddy/v1/responses",
		strings.NewReader(`{"model":"gpt-5-6-sol-low","input":"hi","stream":true,"store":true}`)))
	testutil.Falsef(t, stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: response.completed"), "stream status=%d body=%s", stream.Code, stream.Body.String())
	match := regexp.MustCompile(`"id":"(resp_[0-9a-f]+)"`).FindStringSubmatch(stream.Body.String())
	testutil.Falsef(t, len(match) < 2, "stream carries no response id: %s", stream.Body.String())

	get := httptest.NewRecorder()
	resource(get, httptest.NewRequest(http.MethodGet, "/workbuddy/v1/responses/"+match[1], nil))
	testutil.Falsef(t, get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "hello"), "stored streamed response status=%d body=%s", get.Code, get.Body.String())
}

func TestResponsesDispatcherRoutesByModel(t *testing.T) {
	t.Parallel()

	nativeCalls := 0
	bridgedCalls := 0
	native := func(w http.ResponseWriter, r *http.Request) { nativeCalls++; _, _ = io.WriteString(w, "native") }
	bridged := func(w http.ResponseWriter, r *http.Request) { bridgedCalls++; _, _ = io.WriteString(w, "bridged") }
	dispatch := ModelDispatcher(native, bridged, func(_ context.Context, model string) (bool, error) {
		return strings.HasPrefix(strings.ToLower(model), "grok-"), nil
	})

	call := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		dispatch(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
		return rec
	}

	rec := call(http.MethodPost, "/v1/responses", `{"model":"grok-4.6","input":"hi"}`)
	testutil.Falsef(t, rec.Body.String() != "native", "grok model routed to %q, want native", rec.Body.String())
	rec = call(http.MethodPost, "/v1/responses", `{"model":"gpt-5.6-luna","input":"hi"}`)
	testutil.Falsef(t, rec.Body.String() != "bridged", "non-grok model routed to %q, want bridged", rec.Body.String())
	rec = call(http.MethodGet, "/v1/responses/resp_1", "")
	testutil.Falsef(t, rec.Body.String() != "native", "resource request routed to %q, want the native handler", rec.Body.String())
	testutil.Equal(t, nativeCalls, 2)
	testutil.Equal(t, bridgedCalls, 1)
}

// A channel lookup that fails must not be read as "not a Grok model": the
// bridged handler resolves the channel itself and reports a channel-aware error,
// while the native handler would answer Grok's misleading "model does not
// exist" for a model that simply belongs to another channel.
func TestModelDispatcherSendsLookupFailuresToTheBridgedHandler(t *testing.T) {
	t.Parallel()

	nativeCalls := 0
	bridgedCalls := 0
	native := func(w http.ResponseWriter, r *http.Request) { nativeCalls++; _, _ = io.WriteString(w, "native") }
	bridged := func(w http.ResponseWriter, r *http.Request) { bridgedCalls++; _, _ = io.WriteString(w, "bridged") }
	dispatch := ModelDispatcher(native, bridged, func(context.Context, string) (bool, error) {
		return false, fmt.Errorf("redis unavailable")
	})

	rec := httptest.NewRecorder()
	dispatch(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy-model","input":"hi"}`)))
	testutil.Equal(t, rec.Body.String(), "bridged")
	testutil.Equal(t, nativeCalls, 0)
	testutil.Equal(t, bridgedCalls, 1)
}

// The model that decided the routing is published on the context so downstream
// token accounting and channel resolution reuse it instead of repeating the
// lookup. The hint box is installed by the tracing middleware before the
// dispatcher runs, which is what makes the publish visible to the inner handler.
func TestModelDispatcherPublishesTheResolvedModel(t *testing.T) {
	t.Parallel()

	var seen string
	native := func(w http.ResponseWriter, r *http.Request) {}
	bridged := func(w http.ResponseWriter, r *http.Request) {
		seen = middleware.RequestModelFromContext(r.Context())
	}
	dispatch := ModelDispatcher(native, bridged, func(context.Context, string) (bool, error) {
		return false, nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"GPT-5-6-SOL","messages":[]}`))
	ctx, _ := middleware.RequestModelHint(req.Context())
	dispatch(rec, req.WithContext(ctx))
	testutil.Equal(t, seen, "GPT-5-6-SOL")
}

// An unreadable body is a client-side fault; answering from the native handler
// would blame the model instead.
func TestModelDispatcherRejectsOversizedBodyBeforeHandler(t *testing.T) {
	called := false
	dispatch := ModelDispatcher(func(http.ResponseWriter, *http.Request) { called = true }, func(http.ResponseWriter, *http.Request) { called = true }, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", io.LimitReader(strings.NewReader(strings.Repeat("x", 1024)), 1024))
	req.ContentLength = maxModelDispatcherBodyBytes + 1
	rec := httptest.NewRecorder()
	dispatch(rec, req)
	testutil.Equal(t, rec.Code, http.StatusRequestEntityTooLarge)
	testutil.False(t, called, "oversized request reached a downstream handler")
}

func TestModelDispatcherFailsClosedOnUnreadableBody(t *testing.T) {
	t.Parallel()

	native := func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "native") }
	bridged := func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "bridged") }
	dispatch := ModelDispatcher(native, bridged, func(context.Context, string) (bool, error) {
		return true, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Body = io.NopCloser(failingReader{})
	rec := httptest.NewRecorder()
	dispatch(rec, req)
	testutil.Equal(t, rec.Code, http.StatusBadRequest)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, fmt.Errorf("boom") }
