package responses

import (
	"fmt"
	"io"
	"strings"
)

// CollectToolAliases records the identity behind every tool name in a Build
// payload, so the response side can restore the name the caller declared. The
// alias assigned here is the same one the rewrite uses, because both derive it
// from the same namespace/name pair.
func CollectToolAliases(payload map[string]interface{}) map[string]ToolAliasIdentity {
	aliases := map[string]ToolAliasIdentity{}
	state := NewToolNormalizationState()
	var collect func([]map[string]interface{}, string)
	collect = func(tools []map[string]interface{}, namespace string) {
		for _, tool := range tools {
			kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(tool["type"])))
			switch kind {
			case "namespace":
				collect(InterfaceMaps(tool["tools"]), strings.TrimSpace(fmt.Sprint(tool["name"])))
			case "function":
				name := strings.TrimSpace(fmt.Sprint(tool["name"]))
				if nested, ok := tool["function"].(map[string]interface{}); ok {
					name = strings.TrimSpace(fmt.Sprint(nested["name"]))
				}
				if name != "" && name != "<nil>" {
					aliases[state.Alias(namespace, name)] = ToolAliasIdentity{Kind: "function", Namespace: namespace, Name: name, Declaration: CloneStringInterfaceMap(tool)}
				}
			case "tool_search":
				if strings.EqualFold(strings.TrimSpace(fmt.Sprint(tool["execution"])), "client") {
					aliases["tool_search"] = ToolAliasIdentity{Kind: "tool_search", Name: "tool_search", Declaration: CloneStringInterfaceMap(tool)}
				}
			case "apply_patch":
				aliases["apply_patch"] = ToolAliasIdentity{Kind: "apply_patch", Name: "apply_patch", Declaration: CloneStringInterfaceMap(tool)}
			case "custom":
				name := strings.TrimSpace(fmt.Sprint(tool["name"]))
				if name != "" && name != "<nil>" {
					aliases[BuildToolAlias(namespace, name)] = ToolAliasIdentity{Kind: "custom", Namespace: namespace, Name: name, Declaration: CloneStringInterfaceMap(tool)}
				}
			}
		}
	}
	collect(InterfaceMaps(payload["tools"]), "")
	return aliases
}

// SetData replaces the frame's data lines and drops its raw copy.
//
// A compat layer that rewrote the payload cannot relay the frame as the
// upstream sent it, so the raw bytes must be discarded; writeTo then
// re-renders the frame from the parsed fields.
func (e *SSEEvent) SetData(lines ...string) {
	e.data = lines
	e.raw = nil
}

// WriteTo renders the frame. An untouched frame is written back byte-for-byte
// from its raw copy, so a relayed stream reproduces the upstream exactly.
func (e SSEEvent) WriteFrame(writer io.Writer) error {
	return e.writeTo(writer)
}
