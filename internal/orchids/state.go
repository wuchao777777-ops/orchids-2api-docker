package orchids

import (
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/upstream"
)

type requestState struct {
	preferCodingAgent   bool
	textStarted         bool
	reasoningStarted    bool
	nextBlockIndex      int
	textBlockIndex      int
	reasoningBlockIndex int
	lastTextDelta       string
	lastTextEvent       string
	finishSent          bool
	sawToolCall         bool
	hasFSOps            bool
	stream              bool
	responseStarted     bool
	messageStarted      bool
	modelName           string
	finishReason        string
	inputTokens         int
	outputTokens        int
	suppressStarts      bool
	activeWrites        map[string]*fileWriterState
	errorMsg            string
}

type fileWriterState struct {
	path string
	buf  strings.Builder
}

func cloneRawJSON(data []byte) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	return json.RawMessage(data)
}

func newOrchidsRequestState(req upstream.UpstreamRequest) requestState {
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		modelName = normalizeOrchidsAgentModel(req.Model)
	}
	return requestState{
		stream:              req.Stream,
		modelName:           modelName,
		textBlockIndex:      -1,
		reasoningBlockIndex: -1,
	}
}
