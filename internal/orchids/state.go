package orchids

import (
	"strings"

	"github.com/goccy/go-json"

	"orchids-api/internal/upstream"
)

type requestState struct {
	textStarted         bool
	reasoningStarted    bool
	nextBlockIndex      int
	textBlockIndex      int
	reasoningBlockIndex int
	finishSent          bool
	sawToolCall         bool
	stream              bool
	responseStarted     bool
	messageStarted      bool
	modelName           string
	finishReason        string
	inputTokens         int
	outputTokens        int
	errorMsg            string
	directSSE           upstream.DirectSSEEmitter
	toolMapper          *ToolMapper
	lastPendingToolID   string
	pendingToolInputs   map[string]*orchidsPendingToolInput
	emittedToolCallIDs  map[string]struct{}
}

type orchidsPendingToolInput struct {
	name       string
	blockIndex int
	buf        strings.Builder
}

func cloneRawJSON(data []byte) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	return json.RawMessage(data)
}

func newOrchidsRequestState(req upstream.UpstreamRequest) requestState {
	modelName := normalizeOrchidsAgentModel(req.Model)
	return requestState{
		stream:              req.Stream,
		modelName:           modelName,
		textBlockIndex:      -1,
		reasoningBlockIndex: -1,
		directSSE:           req.DirectSSE,
		toolMapper:          buildClientToolMapper(req.Tools),
	}
}
