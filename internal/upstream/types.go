package upstream

import "orchids-api/internal/prompt"

// UpstreamRequest is the shared request representation for upstream providers.
type UpstreamRequest struct {
	Prompt               string
	ChatHistory          []interface{}
	Model                string
	Stream               bool
	Messages             []prompt.Message
	System               []prompt.SystemItem
	Tools                []interface{}
	NoTools              bool
	NoThinking           bool
	TraceID              string
	Attempt              int
	ChatSessionID        string
	Workdir              string // Dynamic local workdir override
	ProjectID            string
	IsFirstPrompt        bool
	WarpCliAgentModel    string
	WarpComputerUseModel string
}

// SSEMessage is the shared streaming event representation for upstream providers.
type SSEMessage struct {
	Type  string                 `json:"type"`
	Event map[string]interface{} `json:"event,omitempty"`
}
