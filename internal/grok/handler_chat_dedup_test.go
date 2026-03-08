package grok

import (
	"github.com/goccy/go-json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStreamMessageDelta(t *testing.T) {
	tests := []struct {
		name     string
		previous string
		current  string
		want     string
	}{
		{name: "initial", previous: "", current: "你好", want: "你好"},
		{name: "append", previous: "你", current: "你好！", want: "好！"},
		{name: "rewrite prefix", previous: "你！rok，", current: "你好！我是 Grok，xAI AI 助手。", want: "好！我是 Grok，xAI AI 助手。"},
		{name: "shrink", previous: "你好世界", current: "你好", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := streamMessageDelta(tt.previous, tt.current); got != tt.want {
				t.Fatalf("streamMessageDelta(%q,%q)=%q want=%q", tt.previous, tt.current, got, tt.want)
			}
		})
	}
}

func TestCollapseDuplicatedLongChunk(t *testing.T) {
	dup := "Hi! How can I help you today?Hi! How can I help you today?"
	if got := collapseDuplicatedLongChunk(dup); got != "Hi! How can I help you today?" {
		t.Fatalf("collapseDuplicatedLongChunk()=%q", got)
	}

	shortDup := "haha" + "haha"
	if got := collapseDuplicatedLongChunk(shortDup); got != shortDup {
		t.Fatalf("short duplicated chunk should not collapse, got=%q", got)
	}
}

func TestStreamChat_DedupsGreetingRepeat(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()

	dup := "Hi! How can I help you today?Hi! How can I help you today?"
	body := strings.NewReader(
		`{"result":{"response":{"token":"` + dup + `"}}}` +
			`{"result":{"response":{"token":"` + dup + `"}}}`,
	)

	h.streamChat(rec, "grok-420", ModelSpec{ID: "grok-420"}, "", "", true, body, nil)
	contents := extractStreamTextContents(t, rec.Body.String())
	combined := strings.Join(contents, "")
	if strings.Count(combined, "Hi! How can I help you today?") != 1 {
		t.Fatalf("expected greeting once, combined=%q raw=%q", combined, rec.Body.String())
	}
	if strings.Contains(combined, dup) {
		t.Fatalf("unexpected duplicated greeting in stream, combined=%q", combined)
	}
}

func TestStreamChat_PrefersModelResponseOverNoisyTokens(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()

	body := strings.NewReader(
		`{"result":{"response":{"token":"你！rok，"}}}` +
			`{"result":{"response":{"modelResponse":{"message":"你好！我是 Grok，xAI AI 助手。"}}}}` +
			`{"result":{"response":{"modelResponse":{"message":"你好！我是 Grok，xAI AI 助手，不是之前提到的那个。"}}}}`,
	)

	h.streamChat(rec, "grok-420", ModelSpec{ID: "grok-420"}, "", "", true, body, nil)
	combined := strings.Join(extractStreamTextContents(t, rec.Body.String()), "")
	if strings.Contains(combined, "你！rok，") {
		t.Fatalf("unexpected noisy token leak, combined=%q raw=%q", combined, rec.Body.String())
	}
	if !strings.Contains(combined, "你好！我是 Grok，xAI AI 助手，不是之前提到的那个。") {
		t.Fatalf("expected final modelResponse text, combined=%q raw=%q", combined, rec.Body.String())
	}
}

func TestStreamChat_FallsBackToTokenWhenModelResponseMissing(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()

	body := strings.NewReader(
		`{"result":{"response":{"token":"你好"}}}` +
			`{"result":{"response":{"token":"！我是 Grok。"}}}`,
	)

	h.streamChat(rec, "grok-420", ModelSpec{ID: "grok-420"}, "", "", true, body, nil)
	combined := strings.Join(extractStreamTextContents(t, rec.Body.String()), "")
	if !strings.Contains(combined, "你好！我是 Grok。") {
		t.Fatalf("expected token fallback text, combined=%q raw=%q", combined, rec.Body.String())
	}
}

func TestStreamMarkupFilter_CaseInsensitiveToolCard(t *testing.T) {
	filter := &streamMarkupFilter{}
	in := `<XAI:TOOL_USAGE_CARD><XAI:TOOL_NAME>web_search</XAI:TOOL_NAME><XAI:TOOL_ARGS>{"query":"hello"}</XAI:TOOL_ARGS></XAI:TOOL_USAGE_CARD>`
	out := filter.feed(in) + filter.flush()
	if !strings.Contains(out, "[WebSearch] hello") {
		t.Fatalf("expected parsed tool card text, got=%q", out)
	}
}

func TestIndexFoldASCII(t *testing.T) {
	if got := indexFoldASCII("abc<GROK:RENDER>x", "<grok:render"); got != 3 {
		t.Fatalf("indexFoldASCII case-insensitive mismatch got=%d want=3", got)
	}
	if got := indexFoldASCII("abc", "xyz"); got != -1 {
		t.Fatalf("indexFoldASCII should return -1, got=%d", got)
	}
}

func TestStreamTextImageRefCollector_CrossChunkAndDedup(t *testing.T) {
	c := newStreamTextImageRefCollector()
	c.feed("prefix https://assets.grok.com/users/u-1/generated/a1/image.p")
	c.feed("ng?x=1 middle users/u-2/generated/a2/image.we")
	c.feed("bp suffix https://assets.grok.com/users/u-1/generated/a1/image.png?x=1")

	out := make([]string, 0, 4)
	c.emit(func(u string) {
		out = append(out, u)
	})

	wantDirect := "https://assets.grok.com/users/u-1/generated/a1/image.png?x=1"
	wantAsset := "https://assets.grok.com/users/u-2/generated/a2/image.webp"

	hasDirect := false
	hasAsset := false
	directCount := 0
	for _, u := range out {
		if u == wantDirect {
			hasDirect = true
			directCount++
		}
		if u == wantAsset {
			hasAsset = true
		}
	}

	if !hasDirect {
		t.Fatalf("missing direct url, out=%v", out)
	}
	if !hasAsset {
		t.Fatalf("missing asset url, out=%v", out)
	}
	if directCount != 1 {
		t.Fatalf("direct url should be deduped, count=%d out=%v", directCount, out)
	}
}

func extractStreamTextContents(t *testing.T, raw string) []string {
	t.Helper()
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, 4)
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" || strings.TrimSpace(payload) == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			continue
		}
		choices, ok := obj["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		content, _ := delta["content"].(string)
		if strings.TrimSpace(content) != "" {
			out = append(out, content)
		}
	}
	return out
}

func TestAppendChatCompletionChunkMatchesMapEncoding(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		content   string
		finish    string
		hasFinish bool
	}{
		{name: "role", role: "assistant"},
		{name: "content", content: "hello world"},
		{name: "stop", finish: "stop", hasFinish: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := appendChatCompletionChunk(make([]byte, 0, 256), "chatcmpl_1", 123, "grok-4", tt.role, tt.content, tt.finish, tt.hasFinish)
			var got map[string]interface{}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}

			delta := map[string]interface{}{}
			if tt.role != "" {
				delta["role"] = tt.role
			}
			if tt.content != "" {
				delta["content"] = tt.content
			}
			finish := interface{}(nil)
			if tt.hasFinish {
				finish = tt.finish
			}
			wantRaw := encodeJSONBytes(map[string]interface{}{
				"id":      "chatcmpl_1",
				"object":  "chat.completion.chunk",
				"created": int64(123),
				"model":   "grok-4",
				"choices": []map[string]interface{}{{
					"index":         0,
					"delta":         delta,
					"logprobs":      nil,
					"finish_reason": finish,
				}},
			})
			var want map[string]interface{}
			if err := json.Unmarshal(wantRaw, &want); err != nil {
				t.Fatalf("unmarshal want: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got=%#v want=%#v", got, want)
			}
		})
	}
}

func BenchmarkAppendChatCompletionChunk_Content(b *testing.B) {
	buf := make([]byte, 0, 256)
	created := time.Now().Unix()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		raw := appendChatCompletionChunk(buf[:0], "chatcmpl_1", created, "grok-4", "", "hello world", "", false)
		buf = raw[:0]
	}
}

func BenchmarkEncodeChatCompletionChunk_Map(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = encodeJSONBytes(map[string]interface{}{
			"id":      "chatcmpl_1",
			"object":  "chat.completion.chunk",
			"created": int64(123),
			"model":   "grok-4",
			"choices": []map[string]interface{}{{
				"index":         0,
				"delta":         map[string]interface{}{"content": "hello world"},
				"logprobs":      nil,
				"finish_reason": nil,
			}},
		})
	}
}
