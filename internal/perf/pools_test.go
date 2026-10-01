package perf

import (
	"orchids-api/internal/testutil"
	"testing"
)

func TestStringBuilderPool(t *testing.T) {
	sb := AcquireStringBuilder()
	sb.WriteString("hello")
	ReleaseStringBuilder(sb)

	sb2 := AcquireStringBuilder()
	defer ReleaseStringBuilder(sb2)
	testutil.Equal(t, sb2.Len(), 0)
}
