package orchids

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/goccy/go-json"
)

func TestTrimTrailingLineBreakBytes(t *testing.T) {
	if got := string(trimTrailingLineBreakBytes([]byte("hello\r\n"))); got != "hello" {
		t.Fatalf("trimTrailingLineBreakBytes = %q, want %q", got, "hello")
	}
	if got := string(trimTrailingLineBreakBytes([]byte("hello"))); got != "hello" {
		t.Fatalf("trimTrailingLineBreakBytes = %q, want %q", got, "hello")
	}
}

func TestReadLineBytesMatchesExpected(t *testing.T) {
	input := "short line\n" + strings.Repeat("x", 64) + "\nfinal line"
	reader := bufio.NewReaderSize(strings.NewReader(input), 8)

	var got []string
	var scratch []byte
	for {
		line, nextScratch, err := readLineBytes(reader, scratch)
		scratch = nextScratch[:0]
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("readLineBytes error: %v", err)
		}
		if len(line) == 0 && errors.Is(err, io.EOF) {
			break
		}
		got = append(got, string(line))
		if errors.Is(err, io.EOF) {
			break
		}
	}

	want := []string{"short line\n", strings.Repeat("x", 64) + "\n", "final line"}
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func legacyMatchLine(line string, re *regexp.Regexp) bool {
	text := strings.TrimSuffix(line, "\n")
	text = strings.TrimSuffix(text, "\r")
	return re.MatchString(text)
}

func bytesMatchLine(line []byte, re *regexp.Regexp) bool {
	return re.Match(trimTrailingLineBreakBytes(line))
}

func BenchmarkGrepLineMatch_Legacy(b *testing.B) {
	re := regexp.MustCompile(`needle`)
	line := "alpha beta gamma needle delta\r\n"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = legacyMatchLine(line, re)
	}
}

func BenchmarkGrepLineMatch_Bytes(b *testing.B) {
	re := regexp.MustCompile(`needle`)
	line := []byte("alpha beta gamma needle delta\r\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bytesMatchLine(line, re)
	}
}

func BenchmarkBufferedLineRead_String(b *testing.B) {
	data := strings.Repeat("alpha beta gamma needle delta\n", 128)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		reader := bufio.NewReaderSize(strings.NewReader(data), 32)
		for {
			_, err := reader.ReadString('\n')
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferedLineRead_Bytes(b *testing.B) {
	data := strings.Repeat("alpha beta gamma needle delta\n", 128)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		reader := bufio.NewReaderSize(strings.NewReader(data), 32)
		var scratch []byte
		for {
			line, nextScratch, err := readLineBytes(reader, scratch)
			scratch = nextScratch[:0]
			_ = line
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			b.Fatal(err)
		}
	}
}

func TestAppendGrepMatchLineMatchesFmt(t *testing.T) {
	path := `C:/repo/project/file.txt`
	lineNum := 42
	line := []byte("alpha beta gamma needle delta")
	got := string(appendGrepMatchLine(nil, path, lineNum, line))
	want := path + ":42:" + string(line)
	if got != want {
		t.Fatalf("appendGrepMatchLine = %q, want %q", got, want)
	}
}

func BenchmarkAppendGrepMatchLine_Fmt(b *testing.B) {
	path := `C:/repo/project/file.txt`
	lineNum := 42
	line := "alpha beta gamma needle delta"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("%s:%d:%s", path, lineNum, line)
	}
}

func BenchmarkAppendGrepMatchLine_Bytes(b *testing.B) {
	path := `C:/repo/project/file.txt`
	lineNum := 42
	line := []byte("alpha beta gamma needle delta")
	buf := make([]byte, 0, len(path)+len(line)+16)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf = appendGrepMatchLine(buf[:0], path, lineNum, line)
	}
}

func legacyDecodeFSOperation(msg map[string]interface{}) fsOperation {
	raw, _ := json.Marshal(msg)
	var op fsOperation
	_ = json.Unmarshal(raw, &op)
	return op
}

func sampleFSOperationMap() map[string]interface{} {
	return map[string]interface{}{
		"id":            "fs_123",
		"operation":     "grep",
		"path":          "/repo",
		"pattern":       "needle",
		"command":       "echo hi",
		"content":       map[string]interface{}{"text": "hello"},
		"is_background": true,
		"bash_id":       "bash_1",
		"globParameters": map[string]interface{}{
			"path":       "/repo",
			"pattern":    "*.go",
			"maxResults": 20,
		},
		"ripgrepParameters": map[string]interface{}{
			"path":    "/repo",
			"pattern": "needle",
		},
	}
}

func TestDecodeFSOperationMatchesLegacy(t *testing.T) {
	msg := sampleFSOperationMap()
	got := decodeFSOperation(msg)
	want := legacyDecodeFSOperation(msg)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("decodeFSOperation=%s want=%s", gotJSON, wantJSON)
	}
}

func BenchmarkDecodeFSOperation_Legacy(b *testing.B) {
	msg := sampleFSOperationMap()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = legacyDecodeFSOperation(msg)
	}
}

func BenchmarkDecodeFSOperation_Current(b *testing.B) {
	msg := sampleFSOperationMap()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = decodeFSOperation(msg)
	}
}
