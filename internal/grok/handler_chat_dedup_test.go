package grok

import (
	"github.com/goccy/go-json"
	"net/http/httptest"
	"strings"
	"testing"
)

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

	h.streamChat(rec, "grok-420", ModelSpec{ID: "grok-420"}, "", "", true, body)
	contents := extractStreamTextContents(t, rec.Body.String())
	combined := strings.Join(contents, "")
	if strings.Count(combined, "Hi! How can I help you today?") != 1 {
		t.Fatalf("expected greeting once, combined=%q raw=%q", combined, rec.Body.String())
	}
	if strings.Contains(combined, dup) {
		t.Fatalf("unexpected duplicated greeting in stream, combined=%q", combined)
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
