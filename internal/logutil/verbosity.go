package logutil

import "sync/atomic"

var verboseDiagnostics atomic.Bool

func SetVerboseDiagnostics(enabled bool) {
	verboseDiagnostics.Store(enabled)
}

func VerboseDiagnosticsEnabled() bool {
	return verboseDiagnostics.Load()
}
