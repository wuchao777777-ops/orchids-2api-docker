package handler

import (
	"encoding/json"
	"fmt"

	"orchids-api/internal/prompt"
)

// SystemItems supports decoding Anthropic "system" as either a string or an array of items.
type SystemItems []prompt.SystemItem

func (s *SystemItems) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}

	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*s = []prompt.SystemItem{{Type: "text", Text: text}}
		return nil
	}

	var items []prompt.SystemItem
	if err := json.Unmarshal(data, &items); err == nil {
		*s = items
		return nil
	}

	var item prompt.SystemItem
	if err := json.Unmarshal(data, &item); err == nil {
		*s = []prompt.SystemItem{item}
		return nil
	}

	return fmt.Errorf("system must be string or array")
}
