//go:build linux

package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchids-api/internal/buildinfo"
)

func realBinary(t *testing.T) []byte {
	t.Helper()
	path, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return append(b, []byte("updated fixture")...)
}
func TestReplacementAndBackup(t *testing.T) {
	m := fixture(t, realBinary(t))
	op := Operation{ID: "test", Target: buildinfo.Info{Version: "v1.0.3"}, Previous: m.Info}
	if e := m.perform(context.Background(), &op); e != nil {
		t.Fatal(e)
	}
	if op.Phase != "restart_pending" || op.Target.Commit != "abcdef0" {
		t.Fatalf("%+v", op)
	}
	backup, _ := os.ReadFile(op.Backup)
	if string(backup) != "old binary" {
		t.Fatal("backup lost")
	}
	hash, e := hashFile(m.Executable)
	if e != nil || hash != op.SHA256 {
		t.Fatal("new binary not installed")
	}
	if _, e := os.Stat(m.Executable); e != nil {
		t.Fatal("executable path disappeared")
	}
}
func TestFailedReplacementAndWatchdogLaunchPreserveOld(t *testing.T) {
	for _, stage := range []string{"launch", "rename"} {
		t.Run(stage, func(t *testing.T) {
			m := fixture(t, realBinary(t))
			if stage == "launch" {
				m.launch = func(string, string, string, string, string, string) error { return errors.New("launch failed") }
			} else {
				m.rename = func(string, string) error { return errors.New("rename failed") }
			}
			op := Operation{ID: "test", Target: buildinfo.Info{Version: "v1.0.3"}, Previous: m.Info}
			if e := m.perform(context.Background(), &op); e == nil {
				t.Fatal("failure ignored")
			}
			b, _ := os.ReadFile(m.Executable)
			if string(b) != "old binary" {
				t.Fatal("old binary not preserved")
			}
		})
	}
}
func TestLocksAndIdempotencySurviveManagerRecreation(t *testing.T) {
	m := fixture(t, nil)
	op := Operation{ID: "existing", IdempotencyKey: "same-key-123", Kind: "update", Phase: "downloading", Target: buildinfo.Info{Version: "v1.0.3"}}
	if e := m.save(&op); e != nil {
		t.Fatal(e)
	}
	unlock, e := fileLock(filepath.Join(m.Dir, "operation.lock"))
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	if _, e := fileLock(filepath.Join(m.Dir, "operation.lock")); e == nil {
		t.Fatal("second process lock accepted")
	}
	replay, e := m.Start("update", "v1.0.3", "same-key-123")
	if e != nil || replay.ID != op.ID {
		t.Fatalf("idempotency replay failed: %v", e)
	}
	if _, e := m.Start("update", "v1.0.4", "same-key-123"); e == nil {
		t.Fatal("key reused for different target")
	}
	if _, e := m.Start("update", "v1.0.3", "different-key"); e == nil {
		t.Fatal("concurrent update accepted")
	}
}
func TestWatchdogSuccessAndAutomaticRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed-startup"}[fail], func(t *testing.T) {
			m := fixture(t, realBinary(t))
			op := Operation{ID: "watch", Target: buildinfo.Info{Version: "v1.0.3"}, Previous: m.Info}
			if e := m.perform(context.Background(), &op); e != nil {
				t.Fatal(e)
			}
			restarts, checks := 0, 0
			restart := func(string) error { restarts++; return nil }
			verify := func(_, _, hash string, info buildinfo.Info, _ time.Duration) error {
				checks++
				if checks == 1 && fail {
					return errors.New("new version crashed")
				}
				actual, e := hashFile(m.Executable)
				if e != nil || actual != hash {
					return errors.New("incorrect process hash")
				}
				if checks == 1 && info.Version != "v1.0.3" {
					t.Fatal("wrong version verified")
				}
				if checks == 2 && info.Version != "v1.0.2" {
					t.Fatal("wrong recovery version")
				}
				return nil
			}
			if e := runWatchdog(m.statePath(), "fixture.service", m.Executable, "http://127.0.0.1/health", op.ID, restart, verify); e != nil {
				t.Fatal(e)
			}
			result, e := m.Status()
			if e != nil {
				t.Fatal(e)
			}
			if fail {
				if result.Phase != "rolled_back" || restarts != 2 {
					t.Fatalf("%+v restarts=%d", result, restarts)
				}
				b, _ := os.ReadFile(m.Executable)
				if string(b) != "old binary" {
					t.Fatal("recovery did not restore original")
				}
			} else {
				if result.Phase != "complete" || restarts != 1 {
					t.Fatalf("%+v", result)
				}
				if _, e := m.backup(); e != nil {
					t.Fatal("manual rollback unavailable")
				}
			}
		})
	}
}
func TestWatchdogDoesNotActOnAnotherOperation(t *testing.T) {
	m := fixture(t, nil)
	op := Operation{ID: "new-operation", Phase: "restart_pending"}
	m.save(&op)
	if e := runWatchdog(m.statePath(), "unit", m.Executable, "health", "old-operation", func(string) error { t.Fatal("restarted wrong operation"); return nil }, nil); e != nil {
		t.Fatal(e)
	}
}
func TestInvalidELFAndWrongMetadata(t *testing.T) {
	m := fixture(t, []byte("not an ELF"))
	op := Operation{ID: "bad", Target: buildinfo.Info{Version: "v1.0.3"}}
	if e := m.perform(context.Background(), &op); e == nil || !strings.Contains(e.Error(), "ELF") {
		t.Fatalf("%v", e)
	}
	m = fixture(t, realBinary(t))
	m.Source.(*fixtureSource).files["orchids-server-linux-amd64.build-info.txt"] = []byte("version=v9.0.0")
	if e := m.perform(context.Background(), &op); e == nil {
		t.Fatal("wrong target metadata accepted")
	}
}
