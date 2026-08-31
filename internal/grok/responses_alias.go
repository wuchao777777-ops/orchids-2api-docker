package grok

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/goccy/go-json"
)

const maxBuildAliasResponseBytes = 128 << 20

// rewriteBuildToolAliasResponse restores request-scoped namespace and special
// tool identities before a native Build Responses payload crosses the public
// API boundary.
func rewriteBuildToolAliasResponse(source io.ReadCloser, contentType string, aliases map[string]buildToolAliasIdentity) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer source.Close()
		var err error
		if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
			err = rewriteBuildToolAliasSSE(writer, source, aliases)
		} else {
			err = rewriteBuildToolAliasJSONBody(writer, source, aliases)
		}
		_ = writer.CloseWithError(err)
	}()
	return reader
}

func rewriteBuildToolAliasJSONBody(dst io.Writer, source io.Reader, aliases map[string]buildToolAliasIdentity) error {
	raw, err := io.ReadAll(io.LimitReader(source, maxBuildAliasResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxBuildAliasResponseBytes {
		return fmt.Errorf("Grok Build response exceeds 128 MiB")
	}
	converted := rewriteBuildToolAliasesJSON(raw, aliases)
	_, err = dst.Write(converted)
	return err
}

func rewriteBuildToolAliasSSE(dst io.Writer, source io.Reader, aliases map[string]buildToolAliasIdentity) error {
	reader := bufio.NewReaderSize(source, 64*1024)
	state := &buildToolAliasStreamState{calls: map[string]*buildToolAliasStreamCall{}}
	var block bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			block.WriteString(line)
			if strings.TrimSpace(line) == "" {
				converted, emit := rewriteBuildToolAliasSSEBlock(block.String(), aliases, state)
				block.Reset()
				if emit {
					if _, writeErr := io.WriteString(dst, converted); writeErr != nil {
						return writeErr
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if block.Len() > 0 {
					converted, emit := rewriteBuildToolAliasSSEBlock(block.String(), aliases, state)
					if emit {
						_, err = io.WriteString(dst, converted)
					}
				}
				return err
			}
			return err
		}
	}
}

type buildToolAliasStreamCall struct {
	arguments strings.Builder
}

type buildToolAliasStreamState struct {
	calls map[string]*buildToolAliasStreamCall
}

func rewriteBuildToolAliasSSEBlock(block string, aliases map[string]buildToolAliasIdentity, state *buildToolAliasStreamState) (string, bool) {
	if strings.TrimSpace(block) == "" {
		return block, true
	}
	lines := strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n")
	eventType := ""
	dataIndex := -1
	dataPrefix := "data: "
	for index, line := range lines {
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if dataIndex < 0 && strings.HasPrefix(line, "data:") {
			dataIndex = index
			prefixEnd := len("data:")
			for prefixEnd < len(line) && (line[prefixEnd] == ' ' || line[prefixEnd] == '\t') {
				prefixEnd++
			}
			dataPrefix = line[:prefixEnd]
		}
	}
	if dataIndex < 0 {
		return strings.Join(lines, "\n"), true
	}
	data := strings.TrimSpace(strings.TrimPrefix(lines[dataIndex], "data:"))
	if data == "" || data == "[DONE]" {
		return strings.Join(lines, "\n"), true
	}
	var payload map[string]interface{}
	if json.Unmarshal([]byte(data), &payload) != nil {
		return strings.Join(lines, "\n"), true
	}
	if eventType == "" {
		eventType = strings.TrimSpace(fmt.Sprint(payload["type"]))
	}
	if item, _ := payload["item"].(map[string]interface{}); item != nil {
		alias := strings.TrimSpace(fmt.Sprint(item["name"]))
		if identity, ok := aliases[alias]; ok && identity.Kind == "tool_search" {
			id := firstNonEmpty(parseLooseStringAny(item["id"]), parseLooseStringAny(item["call_id"]))
			if id != "" {
				state.calls[id] = &buildToolAliasStreamCall{}
			}
		}
	}
	itemID := firstNonEmpty(parseLooseStringAny(payload["item_id"]), parseLooseStringAny(payload["call_id"]))
	if call := state.calls[itemID]; call != nil {
		switch eventType {
		case "response.function_call_arguments.delta":
			call.arguments.WriteString(parseLooseStringAny(payload["delta"]))
			return "", false
		case "response.function_call_arguments.done":
			if arguments := parseLooseStringAny(payload["arguments"]); arguments != "" {
				call.arguments.Reset()
				call.arguments.WriteString(arguments)
			}
			return "", false
		}
	}
	if eventType == "response.output_item.done" {
		if item, _ := payload["item"].(map[string]interface{}); item != nil {
			id := firstNonEmpty(parseLooseStringAny(item["id"]), parseLooseStringAny(item["call_id"]))
			if call := state.calls[id]; call != nil && call.arguments.Len() > 0 && parseLooseStringAny(item["arguments"]) == "" {
				item["arguments"] = call.arguments.String()
			}
		}
	}
	restoreBuildVisibleTools(payload, aliases)
	rewriteBuildToolAliasValue(payload, aliases)
	converted, err := json.Marshal(payload)
	if err != nil {
		return strings.Join(lines, "\n"), true
	}
	lines[dataIndex] = dataPrefix + string(converted)
	return strings.Join(lines, "\n"), true
}

func rewriteBuildToolAliasesJSON(raw []byte, aliases map[string]buildToolAliasIdentity) []byte {
	var value interface{}
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	restoreBuildVisibleTools(value, aliases)
	rewriteBuildToolAliasValue(value, aliases)
	converted, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return converted
}

func rewriteBuildToolAliasValue(value interface{}, aliases map[string]buildToolAliasIdentity) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, child := range typed {
			rewriteBuildToolAliasValue(child, aliases)
		}
		name := strings.TrimSpace(fmt.Sprint(typed["name"]))
		identity, ok := aliases[name]
		if !ok {
			return
		}
		kind := strings.ToLower(strings.TrimSpace(fmt.Sprint(typed["type"])))
		if !strings.Contains(kind, "function_call") {
			return
		}
		switch identity.Kind {
		case "function":
			typed["name"] = identity.Name
			if identity.Namespace != "" {
				typed["namespace"] = identity.Namespace
			}
		case "tool_search":
			typed["type"] = "tool_search_call"
			typed["execution"] = "client"
			if arguments, ok := typed["arguments"].(string); ok {
				var decoded interface{}
				if json.Unmarshal([]byte(arguments), &decoded) == nil {
					typed["arguments"] = decoded
				}
			}
			delete(typed, "name")
		case "apply_patch":
			typed["type"] = "apply_patch_call"
			delete(typed, "name")
		}
	case []interface{}:
		for _, child := range typed {
			rewriteBuildToolAliasValue(child, aliases)
		}
	}
}

func restoreBuildVisibleTools(value interface{}, aliases map[string]buildToolAliasIdentity) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if tools, ok := typed["tools"].([]interface{}); ok {
			typed["tools"] = restoreBuildToolDeclarations(tools, aliases)
		}
		for key, child := range typed {
			if key != "tools" {
				restoreBuildVisibleTools(child, aliases)
			}
		}
	case []interface{}:
		for _, child := range typed {
			restoreBuildVisibleTools(child, aliases)
		}
	}
}

func restoreBuildToolDeclarations(tools []interface{}, aliases map[string]buildToolAliasIdentity) []interface{} {
	out := make([]interface{}, 0, len(tools))
	namespaceIndexes := map[string]int{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		if tool == nil || !strings.EqualFold(parseLooseStringAny(tool["type"]), "function") {
			out = append(out, raw)
			continue
		}
		identity, ok := aliases[parseLooseStringAny(tool["name"])]
		if !ok {
			out = append(out, raw)
			continue
		}
		declaration := cloneStringInterfaceMap(identity.Declaration)
		if declaration == nil {
			declaration = cloneStringInterfaceMap(tool)
		}
		if identity.Kind != "function" || identity.Namespace == "" {
			out = append(out, declaration)
			continue
		}
		if index, exists := namespaceIndexes[identity.Namespace]; exists {
			namespace := out[index].(map[string]interface{})
			namespace["tools"] = append(interfaceSlice(namespace["tools"]), declaration)
			continue
		}
		namespaceIndexes[identity.Namespace] = len(out)
		out = append(out, map[string]interface{}{"type": "namespace", "name": identity.Namespace, "tools": []interface{}{declaration}})
	}
	return out
}
