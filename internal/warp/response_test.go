package warp

import "testing"

func TestNormalizeToolInputForToolName_ReadRecoversPathFromFallbackField(t *testing.T) {
	t.Parallel()

	got := normalizeToolInputForToolName("Read", `{"field1":"\n8/Users/dailin/Documents/GitHub/TEST/orchids_accounts.txt"}`)
	want := `{"file_path":"/Users/dailin/Documents/GitHub/TEST/orchids_accounts.txt"}`
	if got != want {
		t.Fatalf("normalizeToolInputForToolName(Read) = %q, want %q", got, want)
	}
}

func TestNormalizeToolInputForToolName_ReadKeepsValidPath(t *testing.T) {
	t.Parallel()

	got := normalizeToolInputForToolName("Read", `{"file_path":"/tmp/a.txt","offset":10}`)
	want := `{"file_path":"/tmp/a.txt"}`
	if got != want {
		t.Fatalf("normalizeToolInputForToolName(Read) = %q, want %q", got, want)
	}
}

func TestNormalizeToolInputForToolName_ReadPrefersStableProjectFilesFromList(t *testing.T) {
	t.Parallel()

	got := normalizeToolInputForToolName("Read", `{"files":["/Users/jianxinwei/workspace/cursor-monitor/test_caption_cloud.py","/Users/jianxinwei/workspace/cursor-monitor/README.md","/Users/jianxinwei/workspace/cursor-monitor/requirements.txt"]}`)
	want := `{"file_path":"/Users/jianxinwei/workspace/cursor-monitor/README.md"}`
	if got != want {
		t.Fatalf("normalizeToolInputForToolName(Read) = %q, want %q", got, want)
	}
}

func TestDecodeWarpReadFilesPayload_ReturnsAllUniquePaths(t *testing.T) {
	t.Parallel()

	payload := []byte{
		0x0a, 0x04, 'a', '.', 'p', 'y',
		0x0a, 0x04, 'b', '.', 'p', 'y',
		0x0a, 0x04, 'a', '.', 'p', 'y',
	}

	got := decodeWarpReadFilesPayload(payload)
	if len(got) != 2 || got[0] != "a.py" || got[1] != "b.py" {
		t.Fatalf("decodeWarpReadFilesPayload() = %#v", got)
	}
}

func TestDecodeWarpReadFilesPayload_UnwrapsLengthPrefixedPaths(t *testing.T) {
	t.Parallel()

	pathA := "/Users/dailin/Documents/GitHub/truth_social_scraper/api.py"
	pathB := "/Users/dailin/Documents/GitHub/truth_social_scraper/utils.py"
	payload := append([]byte{0x0a, byte(len(pathA) + 1), byte(len(pathA))}, []byte(pathA)...)
	payload = append(payload, append([]byte{0x0a, byte(len(pathB) + 1), byte(len(pathB))}, []byte(pathB)...)...)

	got := decodeWarpReadFilesPayload(payload)
	if len(got) != 2 || got[0] != pathA || got[1] != pathB {
		t.Fatalf("decodeWarpReadFilesPayload() = %#v", got)
	}
}

func TestBuildWarpReadFileToolCalls_CreatesOneCallPerPath(t *testing.T) {
	t.Parallel()

	got := buildWarpReadFileToolCalls("tool_123", []string{
		"/Users/dailin/Documents/GitHub/truth_social_scraper/api.py",
		"/Users/dailin/Documents/GitHub/truth_social_scraper/monitor_trump.py",
		"/Users/dailin/Documents/GitHub/truth_social_scraper/utils.py",
		"/Users/dailin/Documents/GitHub/truth_social_scraper/dashboard.py",
	})
	if len(got) != 4 {
		t.Fatalf("expected 4 tool calls, got %d: %#v", len(got), got)
	}
	for i, want := range []string{
		`{"file_path":"/Users/dailin/Documents/GitHub/truth_social_scraper/api.py"}`,
		`{"file_path":"/Users/dailin/Documents/GitHub/truth_social_scraper/monitor_trump.py"}`,
		`{"file_path":"/Users/dailin/Documents/GitHub/truth_social_scraper/utils.py"}`,
		`{"file_path":"/Users/dailin/Documents/GitHub/truth_social_scraper/dashboard.py"}`,
	} {
		if got[i].Name != "Read" {
			t.Fatalf("tool call %d name = %q, want Read", i, got[i].Name)
		}
		if got[i].Input != want {
			t.Fatalf("tool call %d input = %q, want %q", i, got[i].Input, want)
		}
	}
	if got[0].ID != "tool_123_1" || got[3].ID != "tool_123_4" {
		t.Fatalf("unexpected tool ids: %#v", got)
	}
}
