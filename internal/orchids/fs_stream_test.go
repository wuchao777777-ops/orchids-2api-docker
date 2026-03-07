package orchids

import (
	"bufio"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
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
