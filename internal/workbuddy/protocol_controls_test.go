package workbuddy

import (
	"encoding/json"
	"orchids-api/internal/upstream"
	"testing"
)

func TestProtocolControlsInWireBody(t *testing.T) {
	zero, limit, parallel := 0.0, 17, false
	req := upstream.UpstreamRequest{Model: "m", MaxTokens: &limit, Temperature: &zero, ParallelToolCalls: &parallel, PromptCacheKey: "cache", ResponseText: map[string]interface{}{"format": map[string]interface{}{"type": "json_schema", "name": "answer", "schema": map[string]interface{}{"type": "object"}, "strict": true}}}
	raw, err := (&Client{}).buildBody(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	schema := body["response_format"].(map[string]interface{})["json_schema"].(map[string]interface{})
	if schema["name"] != "answer" || schema["strict"] != true || body["prompt_cache_key"] != "cache" || body["max_tokens"] != float64(17) || body["temperature"] != float64(0) || body["parallel_tool_calls"] != false {
		t.Fatalf("controls lost: %s", raw)
	}
	req.Include = []string{"reasoning.encrypted_content"}
	if _, err := (&Client{}).buildBody(req); err == nil {
		t.Fatal("unsupported include accepted")
	}
}
