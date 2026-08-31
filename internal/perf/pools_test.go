package perf

import (
	"testing"
)

func TestStringBuilderPool(t *testing.T) {
	sb := AcquireStringBuilder()
	sb.WriteString("hello")
	ReleaseStringBuilder(sb)

	sb2 := AcquireStringBuilder()
	defer ReleaseStringBuilder(sb2)
	if sb2.Len() != 0 {
		t.Fatalf("expected reset builder, got len=%d", sb2.Len())
	}
}
