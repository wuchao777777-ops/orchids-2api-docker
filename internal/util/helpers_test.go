package util

import (
	"context"
	"testing"
	"time"
)

func TestWithDefaultTimeout(t *testing.T) {
	t.Run("creates timeout when none exists", func(t *testing.T) {
		ctx := context.Background()
		newCtx, cancel := WithDefaultTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		if _, ok := newCtx.Deadline(); !ok {
			t.Error("expected deadline to be set")
		}
	})

	t.Run("preserves existing deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		newCtx, newCancel := WithDefaultTimeout(ctx, 100*time.Millisecond)
		defer newCancel()

		deadline1, _ := ctx.Deadline()
		deadline2, _ := newCtx.Deadline()

		if !deadline1.Equal(deadline2) {
			t.Error("expected deadline to be preserved")
		}
	})

	t.Run("returns cancel func when timeout is zero", func(t *testing.T) {
		ctx := context.Background()
		newCtx, cancel := WithDefaultTimeout(ctx, 0)
		defer cancel()

		if _, ok := newCtx.Deadline(); ok {
			t.Error("expected no deadline when timeout is zero")
		}
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
				if result[i] != tt.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

func TestMinInt(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{-1, 1, -1},
		{0, 0, 0},
	}

	for _, tt := range tests {
		result := MinInt(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("MinInt(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
		}
	}
}
