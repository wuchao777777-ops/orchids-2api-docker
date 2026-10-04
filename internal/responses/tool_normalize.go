package responses

import (
	"fmt"
	"strings"

	"encoding/json"
)

func NormalizeTool(tool map[string]interface{}, namespace string, clientSearch, serverSearch bool, param string, state *ToolNormalizationState) ([]map[string]interface{}, error) {
	kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(tool["type"])))
	if kind == "function" {
		if nested, ok := tool["function"].(map[string]interface{}); ok {
			flattened := CloneStringInterfaceMap(nested)
			flattened["type"] = "function"
			tool = flattened
		}
		name := strings.TrimSpace(fmt.Sprint(tool["name"]))
		if name == "" || name == "<nil>" {
			return nil, fmt.Errorf("%s.name is required", param)
		}
		if deferred, _ := tool["defer_loading"].(bool); deferred && clientSearch && !serverSearch {
			return nil, nil
		}
		if deferred, _ := tool["defer_loading"].(bool); deferred && !clientSearch && !serverSearch {
			state.AddWarning("orphan_deferred_tool_loaded")
		}
		out := CloneStringInterfaceMap(tool)
		delete(out, "defer_loading")
		out["name"] = state.Alias(namespace, name)
		if schema, ok := out["parameters"].(map[string]interface{}); ok {
			normalized := normalizeFunctionRoot(schema)
			if !mapsEqualJSON(schema, normalized) {
				state.AddWarning("function_parameters_nullable_root_normalized")
			}
			out["parameters"] = normalized
		}
		return []map[string]interface{}{out}, nil
	}
	if kind == "namespace" {
		name := strings.TrimSpace(fmt.Sprint(tool["name"]))
		children := InterfaceMaps(tool["tools"])
		if name == "" || name == "<nil>" || len(children) == 0 {
			return nil, fmt.Errorf("%s namespace requires name and function tools", param)
		}
		out := make([]map[string]interface{}, 0, len(children))
		for index, child := range children {
			if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(child["type"])), "function") {
				return nil, fmt.Errorf("%s.tools.%d must be a function", param, index)
			}
			items, err := NormalizeTool(child, name, clientSearch, serverSearch, fmt.Sprintf("%s.tools.%d", param, index), state)
			if err != nil {
				return nil, err
			}
			out = append(out, items...)
		}
		return out, nil
	}
	switch kind {
	case "tool_search":
		return nil, nil
	case "apply_patch":
		// The emulated function mirrors the upstream-compatible contract used
		// upstream: one structured V4A operation, so the response side can
		// restore `operation` on the apply_patch_call without parsing a patch.
		state.AddWarning("apply_patch_emulated")
		return []map[string]interface{}{{
			"type": "function", "name": "apply_patch",
			"description": "Create, update, or delete one file using a structured V4A patch operation. " +
				"create_file and update_file require path and diff; delete_file requires path.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"type": map[string]interface{}{"type": "string", "enum": []interface{}{"create_file", "update_file", "delete_file"}},
							"path": map[string]interface{}{"type": "string", "minLength": 1},
							"diff": map[string]interface{}{"type": "string"},
						},
						"required": []interface{}{"type", "path"}, "additionalProperties": false,
					},
				},
				"required": []interface{}{"operation"}, "additionalProperties": false,
			},
			"strict": true,
		}}, nil
	case "local_shell":
		state.AddWarning("local_shell_normalized")
		out := CloneStringInterfaceMap(tool)
		out["type"] = "shell"
		return []map[string]interface{}{out}, nil
	case "web_search_preview", "web_search_preview_2025_03_11", "web_search_2025_08_26":
		state.AddWarning("web_search_controls_downgraded")
		out := CloneStringInterfaceMap(tool)
		out["type"] = "web_search"
		return []map[string]interface{}{out}, nil
	case "custom":
		// A freeform/grammar tool has no upstream equivalent. Emulate it as a
		// function that takes the raw input string, and
		// restore custom_tool_call on the way back.
		name := strings.TrimSpace(fmt.Sprint(tool["name"]))
		if name == "" || name == "<nil>" {
			return nil, fmt.Errorf("%s.name is required", param)
		}
		if _, exists := tool["format"]; exists {
			state.AddWarning("custom_tool_format_downgraded")
		}
		state.AddWarning("custom_tool_emulated")
		description := strings.TrimSpace(ParseLooseStringAny(tool["description"]))
		if description != "" {
			description += "\n"
		}
		description += "Provide the custom tool input in the input string field."
		return []map[string]interface{}{{
			"type": "function", "name": BuildToolAlias(namespace, name), "description": description,
			"parameters": map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"input": map[string]interface{}{"type": "string"}},
				"required":             []interface{}{"input"},
				"additionalProperties": false,
			},
		}}, nil
	case "x_search", "web_search":
		out := CloneStringInterfaceMap(tool)
		if StripWebSearchControlFields(out) {
			state.AddWarning("web_search_controls_downgraded")
		}
		return []map[string]interface{}{out}, nil
	case "mcp", "shell", "image_generation", "collections_search", "file_search", "code_execution", "code_interpreter":
		return []map[string]interface{}{CloneStringInterfaceMap(tool)}, nil
	case "computer_use_preview":
		return nil, fmt.Errorf("%s.type computer_use_preview is not supported by Grok Build", param)
	default:
		return nil, fmt.Errorf("%s.type %q is not supported by Grok Build", param, kind)
	}
}

func normalizeFunctionRoot(schema map[string]interface{}) map[string]interface{} {
	out := CloneStringInterfaceMap(schema)
	if types, ok := out["type"].([]interface{}); ok {
		filtered := make([]interface{}, 0, len(types))
		for _, value := range types {
			if fmt.Sprint(value) != "null" {
				filtered = append(filtered, value)
			}
		}
		if len(filtered) == 1 && fmt.Sprint(filtered[0]) == "object" {
			out["type"] = "object"
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		branches, ok := out[keyword].([]interface{})
		if !ok {
			continue
		}
		kept := make([]interface{}, 0, len(branches))
		for _, raw := range branches {
			branch, _ := raw.(map[string]interface{})
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(branch["type"])), "null") {
				continue
			}
			kept = append(kept, raw)
		}
		if len(kept) == 1 {
			if branch, ok := kept[0].(map[string]interface{}); ok && (branch["type"] == "object" || branch["properties"] != nil) {
				delete(out, keyword)
				for key, value := range branch {
					out[key] = value
				}
				out["type"] = "object"
			}
		}
	}
	// Build requires a visibly object-shaped function root and rejects root
	// unions before resolving their refs. For an already object-constrained
	// root, moving each union into an allOf conjunct is logically equivalent:
	// every object must still satisfy the exact original union and its refs.
	// Keep $defs at the same document root so reference locations stay valid.
	if out["type"] == "object" {
		conjuncts, _ := out["allOf"].([]interface{})
		conjuncts = append([]interface{}(nil), conjuncts...)
		for _, keyword := range []string{"anyOf", "oneOf"} {
			if branches, exists := out[keyword]; exists {
				conjuncts = append(conjuncts, map[string]interface{}{keyword: branches})
				delete(out, keyword)
			}
		}
		if len(conjuncts) > 0 {
			out["allOf"] = conjuncts
		}
	}
	return out
}

func NormalizeToolChoice(payload map[string]interface{}, state *ToolNormalizationState) {
	choice, ok := payload["tool_choice"].(map[string]interface{})
	if !ok {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(choice["type"])))
	if kind == "tool_search" {
		payload["tool_choice"] = map[string]interface{}{"type": "function", "name": "tool_search"}
		state.AddWarning("server_tool_search_choice_downgraded")
		return
	}
	if kind != "function" && kind != "apply_patch" {
		return
	}
	if kind == "apply_patch" {
		payload["tool_choice"] = map[string]interface{}{"type": "function", "name": "apply_patch"}
		return
	}
	name := strings.TrimSpace(ParseLooseStringAny(choice["name"]))
	namespace := strings.TrimSpace(ParseLooseStringAny(choice["namespace"]))
	if nested, ok := choice["function"].(map[string]interface{}); ok {
		name = strings.TrimSpace(fmt.Sprint(nested["name"]))
		namespace = strings.TrimSpace(ParseLooseStringAny(nested["namespace"]))
	}
	if name != "" && name != "<nil>" {
		payload["tool_choice"] = map[string]interface{}{"type": "function", "name": state.Alias(namespace, name)}
	}
}

func mapsEqualJSON(left, right map[string]interface{}) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}
