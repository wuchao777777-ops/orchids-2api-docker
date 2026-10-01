package grok

import (
	"testing"

	"encoding/json"

	"orchids-api/internal/config"
)

// TestResolveModelRetiredIDs pins every identifier the registry must refuse.
// The checkDeprecated rows additionally assert IsDeprecatedModelID, which is what
// lets the management API explain a removal instead of calling the model unknown.
func TestResolveModelRetiredIDs(t *testing.T) {
	cases := []struct {
		id              string
		checkDeprecated bool
		deprecated      bool
	}{
		{id: "grok-4.20-0309", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-0309-non-reasoning-super", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-0309-super", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-0309-reasoning-super", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-0309-non-reasoning-heavy", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-0309-heavy", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-0309-reasoning-heavy", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-fast", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-auto", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-expert", checkDeprecated: true, deprecated: true},
		{id: "grok-4.20-heavy", checkDeprecated: true, deprecated: true},
		{id: "grok-4.3-beta", checkDeprecated: true, deprecated: true},
		{id: "gork-4.20-0309"},
		{id: "grok-4-1-thinking"},
		{id: "grok-imagine-1.0"},
		{id: "grok-4.2"},
		{id: "grok-4-2"},
		{id: "grok-420"},
		{id: "grok-4-20-beta"},
		{id: "grok-imagine-2.0"},
	}
	for _, tc := range cases {
		if _, ok := ResolveModel(tc.id); ok {
			t.Errorf("ResolveModel(%s) = true, want the retired identifier refused", tc.id)
		}
		if tc.checkDeprecated {
			if !IsDeprecatedModelID(tc.id) {
				t.Errorf("IsDeprecatedModelID(%s) = false, want true", tc.id)
			}
		}
	}
}

// TestResolveModelAcceptsCurrentBuildModels is the accepting counterpart: both
// Build models resolve and keep their own id as the upstream model.
func TestResolveModelAcceptsCurrentBuildModels(t *testing.T) {
	for _, id := range []string{"grok-4.5", "grok-4.6"} {
		spec, ok := ResolveModel(id)
		if !ok {
			t.Fatalf("ResolveModel(%s) should succeed", id)
		}
		if spec.UpstreamModel != id {
			t.Fatalf("%s upstream=%q want=%q", id, spec.UpstreamModel, id)
		}
	}
}

func TestGrok45RoutesToBuildCLI(t *testing.T) {
	spec, ok := ResolveModel("grok-4.5")
	if !ok {
		t.Fatal("ResolveModel(grok-4.5) = false, want true")
	}
	if !modelRoutedToCLI(spec, &config.Config{}) {
		t.Fatal("grok-4.5 should route through the official Build CLI OAuth path")
	}
}

func TestLegacyCLIModelListCannotRouteImplicitModel(t *testing.T) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(`{"grok_cli_model_ids":["implicit-model"]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if modelRoutedToCLI(ModelSpec{ID: "implicit-model"}, &cfg) {
		t.Fatal("legacy model list must not route models without explicit Build capability")
	}
	if !modelRoutedToCLI(ModelSpec{ID: "dynamic-build", Upstream: UpstreamCLI}, &cfg) {
		t.Fatal("dynamically discovered Build models must stay routed to CLI")
	}
}

// TestChatCompletionsRequestValidateLeavesSamplingToUpstream pins that Validate
// never invents temperature/top_p defaults; the upstream owns them.
func TestChatCompletionsRequestValidateLeavesSamplingToUpstream(t *testing.T) {
	req := ChatCompletionsRequest{
		Model: "grok-4.20-0309",
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "hello",
		}},
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if req.Temperature != nil {
		t.Fatalf("temperature default mismatch: got=%v", req.Temperature)
	}
	if req.TopP != nil {
		t.Fatalf("top_p default mismatch: got=%v", req.TopP)
	}
}

func TestChatCompletionsRequestValidateToolChoice(t *testing.T) {
	tools := []ToolDef{{
		Type:     "function",
		Function: map[string]interface{}{"name": "weather"},
	}}
	cases := []struct {
		name       string
		tools      []ToolDef
		toolChoice interface{}
		wantErr    bool
	}{
		{name: "required with a declared tool", tools: tools, toolChoice: "required"},
		{name: "unknown literal", tools: tools, toolChoice: "bad-choice", wantErr: true},
		{
			name:  "forced declared function",
			tools: tools,
			toolChoice: map[string]interface{}{
				"type":     "function",
				"function": map[string]interface{}{"name": "weather"},
			},
		},
		{
			name:  "forced unknown function",
			tools: tools,
			toolChoice: map[string]interface{}{
				"type":     "function",
				"function": map[string]interface{}{"name": "unknown_tool"},
			},
			wantErr: true,
		},
		{
			name:  "malformed forced function",
			tools: tools,
			toolChoice: map[string]interface{}{
				"type":     "function",
				"function": map[string]interface{}{},
			},
			wantErr: true,
		},
		{name: "required without tools", toolChoice: "required", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := ChatCompletionsRequest{
				Model:      "grok-4.20-0309",
				Messages:   []ChatMessage{{Role: "user", Content: "hello"}},
				Tools:      tc.tools,
				ToolChoice: tc.toolChoice,
			}
			if err := req.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// TestChatCompletionsRequestUnmarshalLooseTypes keeps the loose scalar decoding
// (stream "true", temperature "1.2") alongside the stream-provided bookkeeping.
func TestChatCompletionsRequestUnmarshalLooseTypes(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantErr      bool
		wantStream   bool
		wantProvided bool
		wantSampling bool
		wantTemp     float64
		wantTopP     float64
	}{
		{
			name:         "loose types",
			raw:          `{"model":"grok-4.20-0309","messages":[{"role":"user","content":"hello"}],"stream":"true","temperature":"1.2","top_p":"0.6"}`,
			wantStream:   true,
			wantProvided: true,
			wantSampling: true,
			wantTemp:     1.2,
			wantTopP:     0.6,
		},
		{
			name:    "invalid stream",
			raw:     `{"model":"grok-4.20-0309","messages":[{"role":"user","content":"hello"}],"stream":"maybe"}`,
			wantErr: true,
		},
		{
			name: "absent stream",
			raw:  `{"model":"grok-4.20-0309","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name:         "explicit stream=false",
			raw:          `{"model":"grok-4.20-0309","messages":[{"role":"user","content":"hello"}],"stream":false}`,
			wantProvided: true,
		},
		{
			name: "null stream",
			raw:  `{"model":"grok-4.20-0309","messages":[{"role":"user","content":"hello"}],"stream":null}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req ChatCompletionsRequest
			err := json.Unmarshal([]byte(tc.raw), &req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unmarshal error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if req.Stream != tc.wantStream {
				t.Fatalf("stream=%v want=%v", req.Stream, tc.wantStream)
			}
			if req.StreamProvided != tc.wantProvided {
				t.Fatalf("stream provided=%v want=%v", req.StreamProvided, tc.wantProvided)
			}
			if (req.Temperature != nil) != tc.wantSampling || (req.TopP != nil) != tc.wantSampling {
				t.Fatalf("sampling present=%v/%v want=%v", req.Temperature != nil, req.TopP != nil, tc.wantSampling)
			}
			if tc.wantSampling && (*req.Temperature != tc.wantTemp || *req.TopP != tc.wantTopP) {
				t.Fatalf("temperature=%v top_p=%v want %v/%v", *req.Temperature, *req.TopP, tc.wantTemp, tc.wantTopP)
			}
		})
	}
}

// TestApplyDefaultChatStream covers the four shapes of the defaulting rule: an
// absent stream (and stream:null) takes the configured default, an explicit value
// survives, and a Stream=false config overrides the default.
func TestApplyDefaultChatStream(t *testing.T) {
	streamFalse := false
	cases := []struct {
		name         string
		configStream *bool
		stream       bool
		provided     bool
		want         bool
	}{
		{name: "absent stream takes the default", want: true},
		{name: "null stream takes the default", want: true},
		{name: "explicit stream=false is not overridden", stream: false, provided: true, want: false},
		{name: "config override", configStream: &streamFalse, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			config.ApplyDefaults(cfg)
			if tc.configStream != nil {
				cfg.Stream = tc.configStream
			}
			h := &Handler{cfg: cfg}
			req := ChatCompletionsRequest{Stream: tc.stream, StreamProvided: tc.provided}
			h.applyDefaultChatStream(&req)
			if req.Stream != tc.want {
				t.Fatalf("stream=%v want=%v", req.Stream, tc.want)
			}
		})
	}
}
