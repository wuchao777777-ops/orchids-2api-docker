package tiktoken

import (
	"strings"
	"testing"
)

func TestEstimateTextTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
		min  int
		max  int
	}{
		{
			name: "Pure English",
			text: "This is a test sentence in English.",
			min:  8,
			max:  12,
		},
		{
			name: "Pure Chinese",
			text: "这是一个测试句子。",
			min:  10,
			max:  15,
		},
		{
			name: "Mixed",
			text: "This is a test 这是测试",
			min:  10,
			max:  16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := EstimateTextTokens(tt.text)
			if tokens < tt.min || tokens > tt.max {
				t.Errorf("EstimateTextTokens(%q) = %d, want between %d and %d", tt.text, tokens, tt.min, tt.max)
			}
		})
	}
}

func TestEstimatorMatchesEstimateTextTokens(t *testing.T) {
	tests := []struct {
		name   string
		parts  []string
		joined string
	}{
		{
			name:   "english split mid word",
			parts:  []string{"hel", "lo ", "wor", "ld!"},
			joined: "hello world!",
		},
		{
			name:   "mixed ascii and cjk",
			parts:  []string{"This ", "is 测", "试 12", "3!"},
			joined: "This is 测试 123!",
		},
		{
			name:   "spaces punctuation and numbers",
			parts:  []string{"foo", "-bar", " 42", ",baz"},
			joined: "foo-bar 42,baz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var estimator Estimator
			for _, part := range tt.parts {
				estimator.Add(part)
			}
			joined := tt.joined
			if joined == "" {
				joined = strings.Join(tt.parts, "")
			}
			want := EstimateTextTokens(joined)
			if got := estimator.Count(); got != want {
				t.Fatalf("count=%d want=%d joined=%q", got, want, joined)
			}
			if !estimator.HasText() {
				t.Fatal("expected estimator to record text")
			}
			estimator.Reset()
			if estimator.Count() != 0 || estimator.HasText() {
				t.Fatal("expected reset estimator")
			}
		})
	}
}

func TestEstimatorAddBytesMatchesEstimateTextTokens(t *testing.T) {
	tests := []string{
		`{"file_path":"/tmp/a.txt","content":"hello world"}`,
		`{"name":"Write","description":"update config and keep comments"}`,
		`{"description":"混合 UTF-8 内容","properties":{"path":{"type":"string"}}}`,
	}

	for _, text := range tests {
		var estimator Estimator
		estimator.AddBytes([]byte(text))
		if got, want := estimator.Count(), EstimateTextTokens(text); got != want {
			t.Fatalf("AddBytes count=%d want=%d text=%q", got, want, text)
		}
	}
}

func BenchmarkEstimateTextTokens_FinalBuilderFlow(b *testing.B) {
	parts := []string{"Write", `{"file_path":"/tmp/a.txt","content":"hello world"}`}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part)
		}
		_ = EstimateTextTokens(builder.String())
	}
}

func BenchmarkEstimateTextTokens_StreamingEstimator(b *testing.B) {
	parts := []string{"Write", `{"file_path":"/tmp/a.txt","content":"hello world"}`}
	var estimator Estimator
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		estimator.Reset()
		for _, part := range parts {
			estimator.Add(part)
		}
		_ = estimator.Count()
	}
}

func BenchmarkEstimateTextTokens_StreamingEstimatorBytes(b *testing.B) {
	parts := [][]byte{[]byte("Write"), []byte(`{"file_path":"/tmp/a.txt","content":"hello world"}`)}
	var estimator Estimator
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		estimator.Reset()
		for _, part := range parts {
			estimator.AddBytes(part)
		}
		_ = estimator.Count()
	}
}
