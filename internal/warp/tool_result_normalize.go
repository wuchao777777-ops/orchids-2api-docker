package warp

import (
	"orchids-api/internal/util"
	"strings"
)

func normalizeWarpToolResultContent(raw string) string {
	text := strings.TrimSpace(stripWarpMetaTags(raw))
	if text == "" {
		return ""
	}
	return util.NormalizePersistedToolResultText(text)
}
