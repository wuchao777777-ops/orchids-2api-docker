package httpclient

import (
	"context"
	"errors"
	"io"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
	"time"
)

type cancelAwareReadCloser struct {
	done <-chan struct{}
}

func (r *cancelAwareReadCloser) Read([]byte) (int, error) {
	<-r.done
	return 0, context.Canceled
}

func (r *cancelAwareReadCloser) Close() error { return nil }

func TestMonitorReadIdleCancelsBlockedReadWithClassifiableError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := MonitorReadIdle(&cancelAwareReadCloser{done: ctx.Done()}, 20*time.Millisecond, cancel, "grok")
	_, err := body.Read(make([]byte, 1))
	testutil.Falsef(t, err == nil || !strings.Contains(err.Error(), "grok stream idle timeout"), "Read() error=%v want Grok idle timeout", err)
	testutil.Falsef(t, !errors.Is(ctx.Err(), context.Canceled), "context error=%v want canceled", ctx.Err())
	_ = body.Close()
}

func TestMonitorReadIdleLeavesDisabledReaderUntouched(t *testing.T) {
	body := io.NopCloser(strings.NewReader("ok"))
	testutil.Equal(t, MonitorReadIdle(body, 0, func() {}, "workbuddy"), body)
}
