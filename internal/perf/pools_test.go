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

func TestMapPool(t *testing.T) {
	m := AcquireMap()
	m["k"] = "v"
	ReleaseMap(m)

	m2 := AcquireMap()
	defer ReleaseMap(m2)
	if len(m2) != 0 {
		t.Fatalf("expected cleared map, got len=%d", len(m2))
	}
}

func TestByteBufferPool(t *testing.T) {
	buf := AcquireByteBuffer()
	_, _ = buf.WriteString("abc")
	ReleaseByteBuffer(buf)

	buf2 := AcquireByteBuffer()
	defer ReleaseByteBuffer(buf2)
	if buf2.Len() != 0 {
		t.Fatalf("expected reset buffer, got len=%d", buf2.Len())
	}
}
