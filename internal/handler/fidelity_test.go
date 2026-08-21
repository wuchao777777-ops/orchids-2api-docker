package handler

import (
	"strings"
	"testing"

	"orchids-api/internal/config"
)

// Fidelity 保真回归：默认配置下，网关不得改写客户端发送的内容。

func TestSanitizeSystemItems_DefaultPreservesVerbatim(t *testing.T) {
	system := SystemItems{
		{Type: "text", Text: "x-anthropic-billing-header: cc_version=2.1.85.351; cc_entrypoint=cli; cch=5e896;"},
		{Type: "text", Text: "cc_entrypoint=claude-code; keep=this"},
		{Type: "text", Text: "You are Claude Code, Anthropic's official CLI for Claude."},
		{Type: "text", Text: "# Environment\n - Primary working directory: C:\\work\n\ngitStatus:\n?? file.txt"},
	}

	// 空配置（未显式设置模式）与显式 "keep" 都必须原样透传。
	for _, cfg := range []*config.Config{nil, {}, {OrchidsCCEntrypointMode: ""}, {OrchidsCCEntrypointMode: "keep"}} {
		got, changed := sanitizeSystemItems(system, false, false, cfg)
		if changed {
			t.Fatalf("cfg=%#v: fidelity default must not change system, got changed=true", cfg)
		}
		if len(got) != len(system) {
			t.Fatalf("cfg=%#v: len=%d want=%d", cfg, len(got), len(system))
		}
		for i := range system {
			if got[i].Text != system[i].Text || got[i].Type != system[i].Type {
				t.Fatalf("cfg=%#v: system[%d] rewritten: %#v", cfg, i, got[i])
			}
		}
	}
}

func TestSanitizeSystemItems_ExplicitStripStillRewrites(t *testing.T) {
	system := SystemItems{
		{Type: "text", Text: "cc_entrypoint=claude-code; keep=this"},
		{Type: "text", Text: "You are Claude Code, Anthropic's official CLI for Claude."},
	}
	got, changed := sanitizeSystemItems(system, false, true, &config.Config{OrchidsCCEntrypointMode: "strip"})
	if !changed {
		t.Fatalf("expected changed=true for explicit strip mode")
	}
	for _, item := range got {
		if strings.Contains(item.Text, "cc_entrypoint=") {
			t.Fatalf("cc_entrypoint not stripped: %q", item.Text)
		}
	}
}
