package adapter

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestToolBlockIndexesSurviveFastAndSlowConversion(t *testing.T) {
	for _, index := range []int{0, 1, 4, 12} {
		for _, event := range []string{"content_block_start", "content_block_delta"} {
			var data string
			if event == "content_block_start" {
				data = fmt.Sprintf(`{"index":%d,"content_block":{"type":"tool_use","id":"call_%d","name":"echo"}}`, index, index)
			} else {
				data = fmt.Sprintf(`{"index":%d,"delta":{"type":"input_json_delta","partial_json":"{}"}}`, index)
			}
			for _, convert := range []func(string, int64, string, []byte) ([]byte, bool){
				buildOpenAIChunkSlow,
				func(id string, created int64, event string, data []byte) ([]byte, bool) {
					return appendOpenAIChunkFast(nil, id, created, event, data)
				},
			} {
				wire, ok := convert("msg", 1, event, []byte(data))
				if !ok {
					t.Fatalf("conversion rejected index %d", index)
				}
				var chunk openAIChunk
				if err := json.Unmarshal(wire, &chunk); err != nil {
					t.Fatal(err)
				}
				if chunk.Choices[0].Delta.ToolCalls[0].Index != index {
					t.Fatalf("tool index lost: %s", wire)
				}
			}
		}
	}
}
