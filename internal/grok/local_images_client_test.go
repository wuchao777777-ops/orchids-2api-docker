package grok

import "testing"

func TestExtractLocalImageGenerationValues(t *testing.T) {
	resp := imagesGenerationsResp{
		Data: []map[string]interface{}{
			{"url": "https://example.com/a.png", "b64_json": "AAA"},
			{"imageUrl": map[string]interface{}{"url": "https://example.com/b.png"}, "base64_data": "BBB"},
			{"image": map[string]interface{}{"fileUrl": "https://example.com/c.png", "base64": "CCC"}},
		},
	}

	gotURL := extractLocalImageGenerationValues(resp, "url")
	if len(gotURL) != 3 || gotURL[0] != "https://example.com/a.png" || gotURL[1] != "https://example.com/b.png" || gotURL[2] != "https://example.com/c.png" {
		t.Fatalf("url values=%v", gotURL)
	}

	gotB64 := extractLocalImageGenerationValues(resp, "b64_json")
	if len(gotB64) != 3 || gotB64[0] != "AAA" || gotB64[1] != "BBB" || gotB64[2] != "CCC" {
		t.Fatalf("b64 values=%v", gotB64)
	}
}
