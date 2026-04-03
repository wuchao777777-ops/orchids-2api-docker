package bolt

import (
	"fmt"
	"strings"

	"orchids-api/internal/prompt"
)

func formatBoltMediaHint(block prompt.ContentBlock) string {
	blockType := strings.ToLower(strings.TrimSpace(block.Type))
	if blockType != "document" && blockType != "image" {
		return ""
	}

	var details []string
	sourceType := ""
	if block.Source != nil {
		if mediaType := strings.TrimSpace(block.Source.MediaType); mediaType != "" {
			details = append(details, mediaType)
		}
		sourceType = strings.TrimSpace(block.Source.Type)
		if sourceType == "" && strings.TrimSpace(block.Source.URL) != "" {
			sourceType = "url"
		}
	}
	if sourceType == "" && strings.TrimSpace(block.URL) != "" {
		sourceType = "url"
	}
	if sourceType != "" {
		details = append(details, "source="+sourceType)
	}
	if len(details) == 0 {
		return fmt.Sprintf("[Attached %s]", blockType)
	}
	return fmt.Sprintf("[Attached %s: %s]", blockType, strings.Join(details, ", "))
}
