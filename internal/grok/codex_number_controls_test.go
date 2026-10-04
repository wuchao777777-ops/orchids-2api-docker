package grok

import (
	"strings"
	"testing"
)

func TestCodexNumberControls(t *testing.T) {
	for _, name := range []string{"exec_command", "write_stdin", "other_tool"} {
		schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"max_output_tokens": map[string]interface{}{"type": "number"},
			"yield_time_ms":     map[string]interface{}{"type": "number"},
			"ratio":             map[string]interface{}{"type": "number"},
		}}
		aliases := map[string]buildToolAliasIdentity{"functions__" + name: {Kind: "function", Name: name, Declaration: map[string]interface{}{"parameters": schema}}}
		got, changed := normalizeAliasedFunctionArguments("functions__"+name, `{"max_output_tokens":4000.0,"yield_time_ms":1e3,"ratio":1.5}`, aliases)
		if name == "other_tool" {
			if changed {
				t.Fatal("unrelated tool modified")
			}
		} else if !changed || !strings.Contains(got, `"max_output_tokens":4000,`) || !strings.Contains(got, `"yield_time_ms":1000`) || !strings.Contains(got, `"ratio":1.5`) {
			t.Fatalf("%s: %s", name, got)
		}
		property := schema["properties"].(map[string]interface{})["max_output_tokens"].(map[string]interface{})
		if property["type"] != "number" {
			t.Fatal("declaration mutated")
		}
		for _, raw := range []string{`{"max_output_tokens":2.5}`, `{"max_output_tokens":1e400}`} {
			if result, changed := normalizeAliasedFunctionArguments("functions__"+name, raw, aliases); changed || result != raw {
				t.Fatalf("unsafe rewrite: %s", result)
			}
		}
	}
}
