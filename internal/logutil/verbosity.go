package logutil

import (
	"log/slog"
	"sync/atomic"
)

var verboseDiagnostics atomic.Bool

func SetVerboseDiagnostics(enabled bool) { verboseDiagnostics.Store(enabled) }

func VerboseDiagnosticsEnabled() bool { return verboseDiagnostics.Load() }

// DebugIf emits a debug line only when the diagnostic switch is on, so each
// guarded dump site costs one call instead of a nested block.
func DebugIf(enabled bool, msg string, args ...any) {
	if enabled {
		slog.Debug(msg, args...)
	}
}
