package perfprobe

import "testing"

// The same bounded scheduler without provider work diagnoses generator stalls.
func BenchmarkOpenLoopControl(b *testing.B) {
	if Load(func() error { return nil }) {
		return
	}
	for i := 0; i < b.N; i++ {
	}
}
