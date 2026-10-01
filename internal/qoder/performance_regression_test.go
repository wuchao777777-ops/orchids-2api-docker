package qoder

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"orchids-api/internal/testutil"
	"orchids-api/internal/upstream"
)

func TestCatalogCacheRefreshesOnlyOnSnapshotChange(t *testing.T) {
	c := NewFromAccount(signedTestAccount(), nil)
	first := c.loadCatalog()
	if c.loadCatalog() != first {
		t.Fatal("unchanged catalog reparsed")
	}
	c.stateMu.Lock()
	c.account.QoderModelIDs = []string{"new-key\tNew Model"}
	c.stateMu.Unlock()
	next := c.loadCatalog()
	if next == first || next.Len() != 1 {
		t.Fatal("changed catalog not refreshed")
	}
	if _, err := first.Resolve("Qwen3.7-Max"); err != nil {
		t.Fatal("previous immutable snapshot mutated")
	}
}

func TestPackedBearerPreservesPayloadAndSignatureIncludingLargePayload(t *testing.T) {
	for _, info := range []string{"encrypted-info", "中文 <>&\n\"", strings.Repeat("large", 2000)} {
		got, err := buildCOSYAuthorization("request", info, "version", "key", "123", []byte("encoded-body"), "/path")
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(got, ".")
		if len(parts) != 3 || parts[0] != "Bearer COSY" {
			t.Fatalf("invalid authorization framing: %q", got)
		}
		raw, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatalf("payload is not standard base64: %v", err)
		}
		var fields map[string]string
		testutil.NoError(t, json.Unmarshal(raw, &fields))
		wantFields := map[string]string{"version": "v1", "requestId": "request", "info": info, "cosyVersion": "version", "ideVersion": ""}
		testutil.Equal(t, len(fields), len(wantFields))
		for key, want := range wantFields {
			if value, ok := fields[key]; !ok || value != want {
				t.Fatalf("payload field %q = %q (present=%t), want %q", key, value, ok, want)
			}
		}
		// Pin wire order and JSON escaping without recreating a payload builder.
		quotedInfo, err := json.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		wantRaw := `{"version":"v1","requestId":"request","info":` + string(quotedInfo) + `,"cosyVersion":"version","ideVersion":""}`
		if string(raw) != wantRaw {
			t.Fatal("packed payload changed field order, escaping, or trailing bytes")
		}
		wantSignature := fmt.Sprintf("%x", md5.Sum([]byte(parts[1]+"\nkey\n123\nencoded-body\n/path")))
		testutil.Equal(t, parts[2], wantSignature)
	}
}

func TestBatchedUUIDsMatchIndependentEntropyReads(t *testing.T) {
	data := make([]byte, 48)
	for i := range data {
		data[i] = byte(i)
	}
	r, s, c, err := newChatUUIDs(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(data)
	for _, got := range []string{r, s, c} {
		want, err := newUUID(reader)
		if err != nil || want != got {
			t.Fatal("UUID format or entropy independence changed")
		}
	}
	if _, _, _, err := newChatUUIDs(bytes.NewReader(data[:47])); err == nil {
		t.Fatal("short entropy accepted")
	}
}

func TestPooledJSONEncodingPreservesWireAndOwnership(t *testing.T) {
	values := []interface{}{
		map[string]interface{}{"prompt": "中文 <>&\n\"", "tools": []interface{}{}, "empty": nil},
		referenceChatContext(upstream.UpstreamRequest{Prompt: "中文"}, modelEntry{Key: "key", IsReasoning: true}),
		map[string]interface{}{"large": string(bytes.Repeat([]byte("x"), 70000))},
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, value := range values {
				raw, err := json.Marshal(value)
				if err != nil {
					t.Error(err)
					return
				}
				got, err := marshalEncodedBody(value)
				if err != nil || !bytes.Equal(got, EncodeBody(raw)) {
					t.Error("pooled encoding changed wire")
					return
				}
				_, _ = marshalEncodedBody(map[string]interface{}{"other": "reuses buffer"})
				if !bytes.Equal(got, EncodeBody(raw)) {
					t.Error("returned body aliases pool")
				}
			}
		}()
	}
	wg.Wait()
	if _, err := marshalEncodedBody(make(chan int)); err == nil {
		t.Fatal("invalid value accepted")
	}
}

func TestPackedAuthHeaderValuesDoNotAliasOnAdd(t *testing.T) {
	c := NewFromAccount(signedTestAccount(), nil)
	req, _ := http.NewRequest(http.MethodPost, "http://mock.invalid/chat", nil)
	testutil.NoError(t, c.applyAuthHeaders(req, credsOf(signedTestAccount()), RuntimeFields{EncryptUserInfo: "info", Key: "key"}, "request", "model", "system", "body", "/chat"))
	want := req.Header.Clone()
	req.Header.Add("Accept", "additional")
	for key, values := range want {
		if key != "Accept" && req.Header.Get(key) != values[0] {
			t.Fatalf("header %s overwritten", key)
		}
	}
}
