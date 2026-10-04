package grok

import (
	"testing"

	"orchids-api/internal/testutil"
)

func TestResponsesImagePartsCarryDefaultDetail(t *testing.T) {
	parts := responsesMessageParts([]interface{}{
		map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/a.png"}},
	}, false)
	testutil.Equal(t, len(parts), 1)
	part, _ := parts[0].(map[string]interface{})
	testutil.Equal(t, part["detail"], "auto")
	// An explicit detail is preserved.
	parts = responsesMessageParts([]interface{}{
		map[string]interface{}{"type": "image_url", "detail": "high", "image_url": map[string]interface{}{"url": "https://example.com/a.png"}},
	}, false)
	part, _ = parts[0].(map[string]interface{})
	testutil.Equal(t, part["detail"], "high")
}
