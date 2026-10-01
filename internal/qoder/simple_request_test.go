package qoder

import (
	"encoding/json"
	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
	"testing"
)

func TestSimplePromptRequestRetainsContextAndExplicitBudget(t *testing.T) {
	for _, limit := range []int{0, 16} {
		req := upstream.UpstreamRequest{Prompt: "测试123123"}
		if limit != 0 {
			req.MaxTokens = &limit
		}
		encoded, err := buildChatBodyProfile(req, modelEntry{Key: "qfmodel"}, "session", "request", "set", DefaultClientVersion, "", sceneBusinessProduct)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := decodeBody(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var body chatBody
		if err = json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 1 || body.Messages[0].Content != req.Prompt || len(body.Tools) != 0 || body.System != "" {
			t.Fatalf("unexpected simple payload: messages=%v tools=%d", body.Messages, len(body.Tools))
		}
		context := body.ChatContext.(map[string]interface{})
		if context["text"] != req.Prompt || context["extra"].(map[string]interface{})["originalContent"] != req.Prompt {
			t.Fatalf("prompt missing from context: %v", body.ChatContext)
		}
		want := 32000
		if limit != 0 {
			want = limit
		}
		parameters := body.Parameters.(map[string]interface{})
		testutil.EqualAny(t, parameters["max_tokens"], float64(want))
	}
}
