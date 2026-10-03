package grok

import (
	"fmt"
	"strings"

	"encoding/json"

	"orchids-api/internal/util"
)

func normalizeBuildTool(tool map[string]interface{}, namespace string, clientSearch, serverSearch bool, param string, state *buildToolNormalizationState) ([]map[string]interface{}, error) {
	kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(tool["type"])))
	if kind == "function" {
		if nested, ok := tool["function"].(map[string]interface{}); ok {
			flattened := cloneStringInterfaceMap(nested)
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
			state.addWarning("orphan_deferred_tool_loaded")
		}
		out := cloneStringInterfaceMap(tool)
		delete(out, "defer_loading")
		out["name"] = state.alias(namespace, name)
		if schema, ok := out["parameters"].(map[string]interface{}); ok {
			normalized := normalizeBuildFunctionRoot(schema)
			if !mapsEqualJSON(schema, normalized) {
				state.addWarning("function_parameters_nullable_root_normalized")
			}
			out["parameters"] = normalized
		}
		return []map[string]interface{}{out}, nil
	}
	if kind == "namespace" {
		name := strings.TrimSpace(fmt.Sprint(tool["name"]))
		children := interfaceMaps(tool["tools"])
		if name == "" || name == "<nil>" || len(children) == 0 {
			return nil, fmt.Errorf("%s namespace requires name and function tools", param)
		}
		out := make([]map[string]interface{}, 0, len(children))
		for index, child := range children {
			if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(child["type"])), "function") {
				return nil, fmt.Errorf("%s.tools.%d must be a function", param, index)
			}
			items, err := normalizeBuildTool(child, name, clientSearch, serverSearch, fmt.Sprintf("%s.tools.%d", param, index), state)
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
		state.addWarning("apply_patch_emulated")
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
		state.addWarning("local_shell_normalized")
		out := cloneStringInterfaceMap(tool)
		out["type"] = "shell"
		return []map[string]interface{}{out}, nil
	case "web_search_preview", "web_search_preview_2025_03_11", "web_search_2025_08_26":
		state.addWarning("web_search_controls_downgraded")
		out := cloneStringInterfaceMap(tool)
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
			state.addWarning("custom_tool_format_downgraded")
		}
		state.addWarning("custom_tool_emulated")
		description := strings.TrimSpace(parseLooseStringAny(tool["description"]))
		if description != "" {
			description += "\n"
		}
		description += "Provide the custom tool input in the input string field."
		return []map[string]interface{}{{
			"type": "function", "name": buildToolAlias(namespace, name), "description": description,
			"parameters": map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"input": map[string]interface{}{"type": "string"}},
				"required":             []interface{}{"input"},
				"additionalProperties": false,
			},
		}}, nil
	case "x_search", "web_search":
		out := cloneStringInterfaceMap(tool)
		if stripWebSearchControlFields(out) {
			state.addWarning("web_search_controls_downgraded")
		}
		return []map[string]interface{}{out}, nil
	case "mcp", "shell", "image_generation", "collections_search", "file_search", "code_execution", "code_interpreter":
		return []map[string]interface{}{cloneStringInterfaceMap(tool)}, nil
	case "computer_use_preview":
		return nil, fmt.Errorf("%s.type computer_use_preview is not supported by Grok Build", param)
	default:
		return nil, fmt.Errorf("%s.type %q is not supported by Grok Build", param, kind)
	}
}

func normalizeBuildFunctionRoot(schema map[string]interface{}) map[string]interface{} {
	out := cloneStringInterfaceMap(schema)
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
	return out
}

func buildToolAlias(namespace, name string) string {
	value := name
	if strings.TrimSpace(namespace) != "" {
		value = namespace + "__" + name
	}
	value = strings.Trim(buildToolAliasInvalid.ReplaceAllString(value, "_"), "_")
	value = util.FirstNonEmptyUntrimmed(value, "tool")
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func normalizeBuildToolChoice(payload map[string]interface{}, state *buildToolNormalizationState) {
	choice, ok := payload["tool_choice"].(map[string]interface{})
	if !ok {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(choice["type"])))
	if kind == "tool_search" {
		payload["tool_choice"] = map[string]interface{}{"type": "function", "name": "tool_search"}
		state.addWarning("server_tool_search_choice_downgraded")
		return
	}
	if kind != "function" && kind != "apply_patch" {
		return
	}
	if kind == "apply_patch" {
		payload["tool_choice"] = map[string]interface{}{"type": "function", "name": "apply_patch"}
		return
	}
	name := strings.TrimSpace(parseLooseStringAny(choice["name"]))
	namespace := strings.TrimSpace(parseLooseStringAny(choice["namespace"]))
	if nested, ok := choice["function"].(map[string]interface{}); ok {
		name = strings.TrimSpace(fmt.Sprint(nested["name"]))
		namespace = strings.TrimSpace(parseLooseStringAny(nested["namespace"]))
	}
	if name != "" && name != "<nil>" {
		payload["tool_choice"] = map[string]interface{}{"type": "function", "name": state.alias(namespace, name)}
	}
}

func mapsEqualJSON(left, right map[string]interface{}) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}
