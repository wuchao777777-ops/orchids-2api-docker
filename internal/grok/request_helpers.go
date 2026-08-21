package grok

import (
	"io"

	"orchids-api/internal/grok/egress"
)

// leaseResponseBody wraps an upstream response body so the egress lease is
// released when the body is closed (or discarded), on every exit path.
type leaseResponseBody struct {
	io.ReadCloser
	release func()
}

func (b *leaseResponseBody) Close() error {
	err := b.ReadCloser.Close()
	if b.release != nil {
		b.release()
		b.release = nil
	}
	return err
}

// egressOutcomeForKind maps a classified upstream error kind to the coarser
// node-health outcome. Persistent challenges degrade the node (it cannot serve
// this request); rate limits and account issues do not.
func egressOutcomeForKind(kind UpstreamErrorKind) egress.FeedbackOutcome {
	switch kind {
	case UpstreamErrorCloudflareChallenge, UpstreamErrorDPoPChallenge:
		return egress.OutcomeChallenge
	case UpstreamErrorRateLimited:
		return egress.OutcomeRateLimited
	case UpstreamErrorAccountBlock:
		return egress.OutcomeAccountBlock
	case UpstreamErrorGenericForbidden:
		return egress.OutcomeForbidden
	default:
		return egress.OutcomeTransportError
	}
}
