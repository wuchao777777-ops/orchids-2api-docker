package store

func BuildWarpSeedModels() []Model {
	return []Model{
		{Channel: "Warp", ModelID: "auto", Name: "Warp Auto", Status: ModelStatusAvailable, IsDefault: true, SortOrder: 0},
		{Channel: "Warp", ModelID: "auto-efficient", Name: "Warp Auto Efficient", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 1},
		{Channel: "Warp", ModelID: "auto-genius", Name: "Warp Auto Genius", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 2},
		{Channel: "Warp", ModelID: "claude-4-5-sonnet", Name: "Claude 4.5 Sonnet (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 3},
		{Channel: "Warp", ModelID: "claude-4-5-sonnet-thinking", Name: "Claude 4.5 Sonnet Thinking (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 4},
		{Channel: "Warp", ModelID: "claude-4-5-opus", Name: "Claude 4.5 Opus (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 5},
		{Channel: "Warp", ModelID: "claude-4-5-opus-thinking", Name: "Claude 4.5 Opus Thinking (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 6},
		{Channel: "Warp", ModelID: "claude-4-6-opus-high", Name: "Claude 4.6 Opus High (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 7},
		{Channel: "Warp", ModelID: "claude-4-6-opus-max", Name: "Claude 4.6 Opus Max (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 8},
		{Channel: "Warp", ModelID: "claude-4-5-haiku", Name: "Claude 4.5 Haiku (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 9},
		{Channel: "Warp", ModelID: "gemini-2-5-pro", Name: "Gemini 2.5 Pro (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 10},
		{Channel: "Warp", ModelID: "gemini-3-pro", Name: "Gemini 3 Pro (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 11},
		{Channel: "Warp", ModelID: "gpt-5-low", Name: "GPT-5 Low (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 12},
		{Channel: "Warp", ModelID: "gpt-5-medium", Name: "GPT-5 Medium (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 13},
		{Channel: "Warp", ModelID: "gpt-5-high", Name: "GPT-5 High (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 14},
		{Channel: "Warp", ModelID: "gpt-5-1-low", Name: "GPT-5.1 Low (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 15},
		{Channel: "Warp", ModelID: "gpt-5-1-medium", Name: "GPT-5.1 Medium (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 16},
		{Channel: "Warp", ModelID: "gpt-5-1-high", Name: "GPT-5.1 High (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 17},
		{Channel: "Warp", ModelID: "gpt-5-1-codex-low", Name: "GPT-5.1 Codex Low (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 18},
		{Channel: "Warp", ModelID: "gpt-5-1-codex-medium", Name: "GPT-5.1 Codex Medium (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 19},
		{Channel: "Warp", ModelID: "gpt-5-1-codex-high", Name: "GPT-5.1 Codex High (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 20},
		{Channel: "Warp", ModelID: "gpt-5-1-codex-max-low", Name: "GPT-5.1 Codex Max Low (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 21},
		{Channel: "Warp", ModelID: "warp-basic", Name: "Warp Basic", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 22},
		{Channel: "Warp", ModelID: "claude-4-6-sonnet-high", Name: "Claude 4.6 Sonnet High (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 23},
		{Channel: "Warp", ModelID: "claude-4-6-sonnet-max", Name: "Claude 4.6 Sonnet Max (Warp)", Status: ModelStatusAvailable, IsDefault: false, SortOrder: 24},
	}
}
