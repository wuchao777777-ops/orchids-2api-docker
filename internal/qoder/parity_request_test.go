package qoder

import (
	"encoding/json"
	"testing"

	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
)

func TestConversationSessionIdentityIsolation(t *testing.T) {
	first := conversationSessionID("account-a", "conversation-a")
	if first != conversationSessionID("account-a", "conversation-a") {
		t.Fatal("session changed across turns")
	}
	if first == conversationSessionID("account-b", "conversation-a") || first == conversationSessionID("account-a", "conversation-b") {
		t.Fatal("sessions leaked between accounts or conversations")
	}
}

func TestToolHistoryRetainsIndexAndReasoningWhitespace(t *testing.T) {
	var messages []prompt.Message
	err := json.Unmarshal([]byte(`[{"role":"assistant","content":"","reasoning_content":" thinking\n","reasoning_item":{"type":"reasoning","summary":[]},"tool_calls":[{"id":"call-a","index":0,"type":"function","function":{"name":"Bash","arguments":"{\"command\":\"dir\"}"}}]},{"role":"tool","tool_call_id":"call-a","content":"file.txt"}]`), &messages)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := buildMessages(upstream.UpstreamRequest{Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Reasoning != " thinking\n" || len(got[0].ReasoningItem) == 0 || len(got[0].ToolCalls) != 1 {
		t.Fatalf("history lost: %+v", got)
	}
	call := got[0].ToolCalls[0]
	if call.Index == nil || *call.Index != 0 || call.Function.Arguments != `{"command":"dir"}` || got[1].ToolCallID != call.ID || got[1].Content != "file.txt" {
		t.Fatalf("tool pairing changed: %+v", got)
	}
	wire, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err = json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["index"]; !ok {
		t.Fatal("zero tool index omitted from wire")
	}
}

func TestTextCacheHintSurvivesWireEncoding(t *testing.T) {
	messages := []prompt.Message{{Role: "user", Content: prompt.MessageContent{Blocks: []prompt.ContentBlock{{Type: "text", Text: "context", CacheControl: &prompt.CacheControl{Type: "ephemeral"}}, {Type: "text", Text: "question"}}}}}
	encoded, err := buildChatBodyProfile(upstream.UpstreamRequest{Messages: messages, Attempt: 2}, modelEntry{Key: "qfmodel"}, "session", "request", "set", DefaultClientVersion, "", sceneBusinessProduct)
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
	if body.IsRetry || len(body.Messages) != 1 || len(body.Messages[0].Contents) != 2 {
		t.Fatalf("unexpected payload: %+v", body)
	}
	parts := body.Messages[0].Contents
	if parts[0].CacheControl == nil || parts[0].CacheControl.Type != "ephemeral" || parts[1].Text != "question" {
		t.Fatalf("cache hint lost: %+v", parts)
	}
}
