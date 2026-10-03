package grok

import (
	"fmt"
	"strings"
)

// normalizeBridgedTools rewrites the tool declarations of a bridged Responses
// request in place and returns the request-scoped identities the response side
// needs to undo the rewrite.
//
// Codex groups its tools: a `namespace` tool carries a name and a list of
// functions, and the model answers with a call whose `namespace` says which
// group the name belongs to. Chat Completions has no grouping — a tool is one
// flat function name — so the grouping is folded into the name here
// (`namespace__function`) and put back on the calls the client sees. Without
// this rewrite a Codex request to WorkBuddy, Qoder or Cline was rejected with
// `tools[7]: tool type "namespace" requires a native Responses provider` before
// it reached an upstream, which made every non-Grok channel unusable from Codex.
//
// A declaration the chat layer genuinely cannot serve is still rejected, with
// the same message as before: forwarding the rest silently would answer a caller
// who asked for an MCP server, a shell or a code interpreter with a model that
// has none of them.
func normalizeBridgedTools(req *ResponsesCreateRequest) (map[string]buildToolAliasIdentity, error) {
	if len(req.Tools) == 0 {
		return nil, nil
	}
	// Hosted search tools are forwarded verbatim; every other non-function,
	// non-namespace declaration is one the chat layer cannot serve at all.
	for index, tool := range req.Tools {
		kind := strings.ToLower(parseLooseStringAny(tool["type"]))
		if kind == "function" || kind == "namespace" || kind == "" {
			continue
		}
		if _, hosted := nativeToolTypes[kind]; hosted {
			continue
		}
		return nil, fmt.Errorf("tools[%d]: tool type %q requires a native Responses provider", index, kind)
	}
	state := newBuildToolNormalizationState()
	normalized := make([]map[string]interface{}, 0, len(req.Tools))
	for index, tool := range req.Tools {
		items, err := normalizeBuildTool(tool, "", false, false, fmt.Sprintf("tools[%d]", index), state)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, items...)
	}
	// The alias walk needs the declarations the caller sent: the rewritten list
	// has already had the namespace folded into each name, so walking it would
	// record the flat name as if it were the declared one.
	aliases := bridgeToolAliases(req.Tools, state)
	req.Tools = normalized
	req.ToolChoice = normalizeBridgeToolChoice(req.ToolChoice, state)
	lowerBridgedInputToolNames(req.Input, state)
	return aliases, nil
}

// bridgeToolAliases records the identity behind every flattened name. It walks
// the original declarations: a namespace's children were flattened in the order
// they appear, and state.lookup returns the name each one was actually given.
func bridgeToolAliases(tools []map[string]interface{}, state *buildToolNormalizationState) map[string]buildToolAliasIdentity {
	aliases := map[string]buildToolAliasIdentity{}
	var walk func([]map[string]interface{}, string)
	walk = func(items []map[string]interface{}, namespace string) {
		for _, tool := range items {
			kind := strings.ToLower(parseLooseStringAny(tool["type"]))
			if kind == "namespace" {
				walk(interfaceMaps(tool["tools"]), parseLooseStringAny(tool["name"]))
				continue
			}
			if kind != "function" {
				continue
			}
			name := parseLooseStringAny(tool["name"])
			if nested, ok := tool["function"].(map[string]interface{}); ok {
				name = parseLooseStringAny(nested["name"])
			}
			if name == "" || name == "<nil>" {
				continue
			}
			aliases[state.lookup(namespace, name)] = buildToolAliasIdentity{
				Kind: "function", Namespace: namespace, Name: name, Declaration: cloneStringInterfaceMap(tool),
			}
		}
	}
	walk(tools, "")
	return aliases
}

// lowerBridgedInputToolNames maps the calls a client echoes back onto the flat
// names the chat layer declared: a namespaced history item that kept its short
// name would reach the upstream as a call to a tool it never saw.
func lowerBridgedInputToolNames(input interface{}, state *buildToolNormalizationState) {
	items, ok := input.([]interface{})
	if !ok {
		return
	}
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if !strings.EqualFold(parseLooseStringAny(item["type"]), "function_call") {
			continue
		}
		name := parseLooseStringAny(item["name"])
		if name == "" {
			continue
		}
		namespace := parseLooseStringAny(item["namespace"])
		if alias := state.lookup(namespace, name); alias != "" && alias != name {
			item["name"] = alias
			delete(item, "namespace")
		}
	}
}

// normalizeBridgeToolChoice points a `tool_choice` at the flattened name of the
// function it names, so forcing a namespaced function still reaches the upstream.
func normalizeBridgeToolChoice(choice interface{}, state *buildToolNormalizationState) interface{} {
	typed, ok := choice.(map[string]interface{})
	if !ok {
		return choice
	}
	name := parseLooseStringAny(typed["name"])
	namespace := parseLooseStringAny(typed["namespace"])
	if nested, ok := typed["function"].(map[string]interface{}); ok {
		name = parseLooseStringAny(nested["name"])
		namespace = parseLooseStringAny(nested["namespace"])
	}
	if !strings.EqualFold(parseLooseStringAny(typed["type"]), "function") {
		return choice
	}
	if alias := state.lookup(namespace, name); alias != "" {
		return map[string]interface{}{"type": "function", "name": alias}
	}
	return choice
}

// restoreBridgeToolName is the restore for a name on its own: the short name a
// flat name stands for, or the name itself when the bridge never aliased it.
func restoreBridgeToolName(name string, aliases map[string]buildToolAliasIdentity) string {
	identity, ok := aliases[strings.TrimSpace(name)]
	if !ok || identity.Kind != "function" {
		return name
	}
	return identity.Name
}

// restoreBridgeToolCall puts the identity the caller declared back on one tool
// call: the short name, the namespace it belongs to, and — for a strict client
// decoder — integer arguments the model returned as floats. flatName is the
// name the chat layer used; item is mutated in place.
func restoreBridgeToolCall(item map[string]interface{}, flatName string, aliases map[string]buildToolAliasIdentity) {
	if item == nil {
		return
	}
	identity, ok := aliases[strings.TrimSpace(flatName)]
	if !ok || identity.Kind != "function" {
		return
	}
	item["name"] = identity.Name
	if identity.Namespace != "" {
		item["namespace"] = identity.Namespace
	}
	if arguments, ok := item["arguments"].(string); ok {
		if normalized, changed := normalizeFunctionArguments(arguments, aliasParameterSchema(identity)); changed {
			item["arguments"] = normalized
		}
	}
}

// restoreBridgeToolIdentity undoes the flattening on a whole response object,
// so every call in it carries the name the client declared. A caller that
// declared no grouped tool gets the response untouched.
func restoreBridgeToolIdentity(response map[string]interface{}, aliases map[string]buildToolAliasIdentity) {
	if len(aliases) == 0 {
		return
	}
	restoreBridgeToolItems(response["output"], aliases)
}

func restoreBridgeToolItems(value interface{}, aliases map[string]buildToolAliasIdentity) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if key == "arguments" || key == "name" || key == "namespace" {
				continue
			}
			restoreBridgeToolItems(child, aliases)
		}
		if !strings.Contains(strings.ToLower(parseLooseStringAny(typed["type"])), "function_call") {
			return
		}
		restoreBridgeToolCall(typed, parseLooseStringAny(typed["name"]), aliases)
	case []interface{}:
		for _, child := range typed {
			restoreBridgeToolItems(child, aliases)
		}
	}
}
