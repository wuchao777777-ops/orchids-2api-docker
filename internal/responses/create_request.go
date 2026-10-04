package responses

import (
	"fmt"
	"strconv"
	"strings"

	"encoding/json"
)

// CreateRequest is the OpenAI Responses create request as a client sends it.
//
// It is decoded in two passes (see UnmarshalJSON): the members whose wire shape
// is already the one the gateway consumes arrive through the embedded struct,
// decoded exactly as declared, so a malformed value is a request error instead
// of a silently dropped field. The scalar members accept several JSON shapes
// ("true", "1", a numeric string), so interface{} members shadow the typed ones
// and are parsed loosely afterwards.
type CreateRequest struct {
	Model              string                   `json:"model"`
	Input              interface{}              `json:"input"`
	Instructions       string                   `json:"instructions,omitempty"`
	Stream             bool                     `json:"stream,omitempty"`
	StreamProvided     bool                     `json:"-"`
	Reasoning          map[string]interface{}   `json:"reasoning,omitempty"`
	Temperature        *float64                 `json:"temperature,omitempty"`
	TopP               *float64                 `json:"top_p,omitempty"`
	MaxOutputTokens    *int                     `json:"max_output_tokens,omitempty"`
	Tools              []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice         interface{}              `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool                    `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string                   `json:"previous_response_id,omitempty"`
	Store              *bool                    `json:"store,omitempty"`
	Metadata           map[string]interface{}   `json:"metadata,omitempty"`
	Truncation         string                   `json:"truncation,omitempty"`
	Include            []string                 `json:"include,omitempty"`
	Background         *bool                    `json:"background,omitempty"`
	PromptCacheKey     string                   `json:"prompt_cache_key,omitempty"`
	// Text carries the Responses text controls, whose only member is the output
	// format (`text.format`). The chat-only channels express the same thing as
	// `response_format`, so the bridge passes the object through unchanged and
	// lets the chat layer decide how much of it the upstream honors.
	Text map[string]interface{} `json:"text,omitempty"`
	// ResponseFormat accepts the chat-shaped field as well. Clients that were
	// written against the chat API and later migrated to Responses keep sending
	// it, and silently dropping it used to turn a structured-output request into
	// free-form prose.
	ResponseFormat map[string]interface{} `json:"response_format,omitempty"`
}

func (r *CreateRequest) UnmarshalJSON(data []byte) error {
	type plainCreateRequest CreateRequest
	type rawCreateRequest struct {
		plainCreateRequest
		Model              interface{} `json:"model"`
		Instructions       interface{} `json:"instructions,omitempty"`
		Stream             interface{} `json:"stream,omitempty"`
		Temperature        interface{} `json:"temperature,omitempty"`
		TopP               interface{} `json:"top_p,omitempty"`
		MaxOutputTokens    interface{} `json:"max_output_tokens,omitempty"`
		ParallelToolCalls  interface{} `json:"parallel_tool_calls,omitempty"`
		PreviousResponseID interface{} `json:"previous_response_id,omitempty"`
		Store              interface{} `json:"store,omitempty"`
		Truncation         interface{} `json:"truncation,omitempty"`
		Background         interface{} `json:"background,omitempty"`
		PromptCacheKey     interface{} `json:"prompt_cache_key,omitempty"`
	}

	var raw rawCreateRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	stream, err := ParseLooseBoolAny(raw.Stream)
	if err != nil {
		return err
	}
	temp, err := ParseLooseFloatAny(raw.Temperature)
	if err != nil {
		return err
	}
	topP, err := ParseLooseFloatAny(raw.TopP)
	if err != nil {
		return err
	}
	maxOutputTokens, err := ParseLooseIntAny(raw.MaxOutputTokens)
	if err != nil {
		return err
	}
	var rawMap map[string]json.RawMessage
	_ = json.Unmarshal(data, &rawMap)
	_, streamProvided := rawMap["stream"]
	var parallel *bool
	if _, ok := rawMap["parallel_tool_calls"]; ok {
		v, err := ParseLooseBoolAnyForField(raw.ParallelToolCalls, "parallel_tool_calls")
		if err != nil {
			return err
		}
		parallel = &v
	}
	var store *bool
	if _, ok := rawMap["store"]; ok {
		v, err := ParseLooseBoolAnyForField(raw.Store, "store")
		if err != nil {
			return err
		}
		store = &v
	}
	var background *bool
	if _, ok := rawMap["background"]; ok {
		v, err := ParseLooseBoolAnyForField(raw.Background, "background")
		if err != nil {
			return err
		}
		background = &v
	}
	var maxOutput *int
	if _, ok := rawMap["max_output_tokens"]; ok {
		maxOutput = &maxOutputTokens
	}

	*r = CreateRequest(raw.plainCreateRequest)
	r.Model = ParseLooseStringAny(raw.Model)
	r.Instructions = ParseLooseStringAny(raw.Instructions)
	r.Stream = stream
	r.StreamProvided = streamProvided
	r.Temperature = temp
	r.TopP = topP
	r.MaxOutputTokens = maxOutput
	r.ParallelToolCalls = parallel
	r.PreviousResponseID = ParseLooseStringAny(raw.PreviousResponseID)
	r.Store = store
	r.Truncation = ParseLooseStringAny(raw.Truncation)
	r.Background = background
	r.PromptCacheKey = ParseLooseStringAny(raw.PromptCacheKey)
	return nil
}

// ParseLooseBoolAnyForField reads a boolean that a client may send as a bool,
// a number or a word, naming the field in the error so the caller can quote it.
func ParseLooseBoolAnyForField(value interface{}, field string) (bool, error) {
	if strings.TrimSpace(field) == "" {
		field = "value"
	}
	errText := field + " must be a boolean"
	switch v := value.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return false, nil
		}
		switch strings.ToLower(raw) {
		case "1", "true", "yes", "y", "on":
			return true, nil
		case "0", "false", "no", "n", "off":
			return false, nil
		default:
			return false, fmt.Errorf("%s", errText)
		}
	case float64:
		if v == 1 {
			return true, nil
		}
		if v == 0 {
			return false, nil
		}
		return false, fmt.Errorf("%s", errText)
	default:
		return false, fmt.Errorf("%s", errText)
	}
}

// ParseLooseBoolAny is ParseLooseBoolAnyForField for the `stream` field.
func ParseLooseBoolAny(value interface{}) (bool, error) {
	return ParseLooseBoolAnyForField(value, "stream")
}

// ParseLooseIntAny reads an integer that a client may send as a number or a
// numeric string.
func ParseLooseIntAny(value interface{}) (int, error) {
	switch v := value.(type) {
	case nil:
		return 0, nil
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, err
		}
		return n, nil
	default:
		return 0, fmt.Errorf("invalid integer value")
	}
}

// ParseLooseFloatAny reads an optional float: nil stays nil, so a caller can
// tell "absent" from "zero".
func ParseLooseFloatAny(value interface{}) (*float64, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case float64:
		out := v
		return &out, nil
	case int:
		out := float64(v)
		return &out, nil
	case int32:
		out := float64(v)
		return &out, nil
	case int64:
		out := float64(v)
		return &out, nil
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return nil, nil
		}
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, err
		}
		return &n, nil
	default:
		return nil, fmt.Errorf("invalid float value")
	}
}
