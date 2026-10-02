package qoder

import (
	"encoding/json"
	"orchids-api/internal/upstream"
	"testing"
)

func TestProtocolControlsInWireParameters(t *testing.T) {
	req := upstream.UpstreamRequest{Model: "m", Prompt: "hi", ResponseFormat: map[string]interface{}{"type": "json_object"}}
	raw, err := buildChatBodyProfile(req, modelEntry{Key: "m"}, "s", "r", "set", "1", "user", "product")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(decoded), &body); err != nil {
		t.Fatal(err)
	}
	parameters := body["parameters"].(map[string]interface{})
	if parameters["response_format"].(map[string]interface{})["type"] != "json_object" {
		t.Fatalf("format lost: %s", decoded)
	}
	req.PromptCacheKey = "cache"
	if _, err := buildChatBodyProfile(req, modelEntry{Key: "m"}, "s", "r", "set", "1", "user", "product"); err == nil {
		t.Fatal("unsupported cache key accepted")
	}
}
