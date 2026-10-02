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
	"orchids-api/internal/testutil"
)

func realBinary(t *testing.T) []byte {
	t.Helper()
	path, e := os.Executable()
	testutil.NoError(t, e)
	b, e := os.ReadFile(path)
	testutil.NoError(t, e)
	return append(b, []byte("updated fixture")...)
}
func TestReplacementAndBackup(t *testing.T) {
	m := fixture(t, realBinary(t))
	op := Operation{ID: "test", Target: buildinfo.Info{Version: "v1.0.3"}, Previous: m.Info}
	e := m.perform(context.Background(), &op)
	testutil.NoError(t, e)
	testutil.Equal(t, op.Phase, "restart_pending")
	testutil.Equal(t, op.Target.Commit, "abcdef0")
	backup, _ := os.ReadFile(op.Backup)
	testutil.Equal(t, string(backup), "old binary")
	hash, e := hashFile(m.Executable)
	testutil.False(t, e != nil || hash != op.SHA256, "new binary not installed")
	_, e = os.Stat(m.Executable)
	testutil.NoError(t, e)
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
			e := m.perform(context.Background(), &op)
			testutil.Error(t, e)
			b, _ := os.ReadFile(m.Executable)
			testutil.Equal(t, string(b), "old binary")
		})
	}
}
func TestLocksAndIdempotencySurviveManagerRecreation(t *testing.T) {
	m := fixture(t, nil)
	op := Operation{ID: "existing", IdempotencyKey: "same-key-123", Kind: "update", Phase: "downloading", Target: buildinfo.Info{Version: "v1.0.3"}}
	e := m.save(&op)
	testutil.NoError(t, e)
	unlock, e := fileLock(filepath.Join(m.Dir, "operation.lock"))
	testutil.NoError(t, e)
	defer unlock()
	_, e = fileLock(filepath.Join(m.Dir, "operation.lock"))
	testutil.Error(t, e)
	replay, e := m.Start("update", "v1.0.3", "same-key-123")
	testutil.Equal(t, e, nil)
	testutil.Equal(t, replay.ID, op.ID)
	_, e = m.Start("update", "v1.0.4", "same-key-123")
	testutil.Error(t, e)
	_, e = m.Start("update", "v1.0.3", "different-key")
	testutil.Error(t, e)
}
func TestWatchdogSuccessAndAutomaticRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed-startup"}[fail], func(t *testing.T) {
			m := fixture(t, realBinary(t))
			op := Operation{ID: "watch", Target: buildinfo.Info{Version: "v1.0.3"}, Previous: m.Info}
			e := m.perform(context.Background(), &op)
			testutil.NoError(t, e)
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
				testutil.False(t, checks == 1 && info.Version != "v1.0.3", "wrong version verified")
				testutil.False(t, checks == 2 && info.Version != "v1.0.2", "wrong recovery version")
				return nil
			}
			e = runWatchdog(m.statePath(), "fixture.service", m.Executable, "http://127.0.0.1/health", op.ID, restart, verify)
			testutil.NoError(t, e)
			result, e := m.Status()
			testutil.NoError(t, e)
			if fail {
				testutil.Equal(t, result.Phase, "rolled_back")
				testutil.Equal(t, restarts, 2)
				b, _ := os.ReadFile(m.Executable)
				testutil.Equal(t, string(b), "old binary")
			} else {
				testutil.Equal(t, result.Phase, "complete")
				testutil.Equal(t, restarts, 1)
				_, e := m.backup()
				testutil.NoError(t, e)
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
	e := m.perform(context.Background(), &op)
	testutil.Falsef(t, e == nil || !strings.Contains(e.Error(), "ELF"), "%v", e)
	m = fixture(t, realBinary(t))
	m.Source.(*fixtureSource).files["orchids-server-linux-amd64.build-info.txt"] = []byte("version=v9.0.0")
	e = m.perform(context.Background(), &op)
	testutil.Error(t, e)
}
