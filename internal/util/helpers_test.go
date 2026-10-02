package util

import (
	"context"
	"orchids-api/internal/testutil"
	"testing"
	"time"
)

func TestWithDefaultTimeout(t *testing.T) {
	t.Run("creates timeout when none exists", func(t *testing.T) {
		ctx := context.Background()
		newCtx, cancel := WithDefaultTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		_, ok := newCtx.Deadline()
		testutil.CheckFalse(t, !ok, "expected deadline to be set")
	})

	t.Run("preserves existing deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		newCtx, newCancel := WithDefaultTimeout(ctx, 100*time.Millisecond)
		defer newCancel()

		deadline1, _ := ctx.Deadline()
		deadline2, _ := newCtx.Deadline()

		testutil.CheckFalse(t, !deadline1.Equal(deadline2), "expected deadline to be preserved")
	})

	t.Run("returns cancel func when timeout is zero", func(t *testing.T) {
		ctx := context.Background()
		newCtx, cancel := WithDefaultTimeout(ctx, 0)
		defer cancel()

		_, ok := newCtx.Deadline()
		testutil.CheckFalse(t, ok, "expected no deadline when timeout is zero")
	})
}

func TestUniqueStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: nil,
		},
		{
			name:     "no duplicates",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with duplicates",
			input:    []string{"a", "b", "a", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with empty strings",
			input:    []string{"a", "", "b", "", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with whitespace",
			input:    []string{" a ", "a", "  b  ", "b"},
			expected: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := UniqueStrings(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected length %d, got %d", len(tt.expected), len(result))
				return
			}
			for i := range result {
				testutil.CheckEqual(t, result[i], tt.expected[i])
			}
		})
	}
}

func TestSecureCompare(t *testing.T) {
	testutil.False(t, !SecureCompare("secret", "secret"), "equal values should match")
	testutil.False(t, SecureCompare("secret", "different"), "different values should not match")
}
