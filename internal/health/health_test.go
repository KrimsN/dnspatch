package health

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/runner"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecorderAfterCycleCreatesAFilePerInstance(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir, discardLogger())
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	rec.AfterCycle(context.Background(), runner.CycleEvent{Instance: "home", Success: true})
	rec.AfterCycle(context.Background(), runner.CycleEvent{Instance: "office", Success: false})

	for _, name := range []string{"home", "office"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("status file for %q: %v", name, err)
		}
	}
}

func TestRecorderRecordsBothSuccessAndFailure(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir, discardLogger())
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	// A failed cycle is still activity: the tick loop ran, only a provider
	// failed, which backoff and logging already report.
	rec.AfterCycle(context.Background(), runner.CycleEvent{Instance: "home", Success: false})

	if err := CheckAll(dir, []config.Instance{{Name: "home", Interval: time.Minute}}, time.Now()); err != nil {
		t.Errorf("CheckAll = %v, want nil: a failed cycle still counts as liveness", err)
	}
}

func TestCheckAllFailsWhenNoFileWasEverWritten(t *testing.T) {
	dir := t.TempDir()

	err := CheckAll(dir, []config.Instance{{Name: "home", Interval: time.Minute}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), `instance "home"`) {
		t.Errorf("CheckAll = %v, want an error naming the instance", err)
	}
}

func TestCheckAllFailsWhenTheFileIsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "home")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	err := CheckAll(dir, []config.Instance{{Name: "home", Interval: time.Minute}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "ago") {
		t.Errorf("CheckAll = %v, want it to report the stale file's age", err)
	}
}

func TestCheckAllUsesTheInstanceOwnInterval(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	for name, age := range map[string]time.Duration{"fast": 90 * time.Second, "slow": 90 * time.Second} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		when := now.Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}

	instances := []config.Instance{
		{Name: "fast", Interval: 30 * time.Second}, // threshold 60s: 90s old is stale
		{Name: "slow", Interval: time.Hour},        // threshold 2h: 90s old is fine
	}

	err := CheckAll(dir, instances, now)
	if err == nil || !strings.Contains(err.Error(), `instance "fast"`) {
		t.Fatalf(`CheckAll = %v, want an error naming "fast"`, err)
	}
	if strings.Contains(err.Error(), `instance "slow"`) {
		t.Errorf("CheckAll = %v, must not flag \"slow\": its own interval gives it a longer threshold", err)
	}
}

func TestCheckAllJoinsAllStuckInstances(t *testing.T) {
	dir := t.TempDir()

	instances := []config.Instance{
		{Name: "a", Interval: time.Minute},
		{Name: "b", Interval: time.Minute},
	}

	err := CheckAll(dir, instances, time.Now())
	if err == nil || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("CheckAll = %v, want both instances named", err)
	}
}

func TestFileNameRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir, discardLogger())
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	rec.AfterCycle(context.Background(), runner.CycleEvent{Instance: "..", Success: true})
	rec.AfterCycle(context.Background(), runner.CycleEvent{Instance: "../escaped", Success: true})

	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "escaped")); statErr == nil {
		t.Fatal("a status file escaped the health directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("entries in the health dir = %d, want 2 (both writes stayed inside it)", len(entries))
	}
}

func TestResolveDirUsesTheEnvironmentVariableWhenSet(t *testing.T) {
	t.Setenv(EnvDir, "/custom/health/dir")
	if got := ResolveDir(); got != "/custom/health/dir" {
		t.Errorf("ResolveDir = %q, want the env value", got)
	}
}

func TestResolveDirFallsBackToDefaultDir(t *testing.T) {
	t.Setenv(EnvDir, "")
	if got := ResolveDir(); got != DefaultDir() {
		t.Errorf("ResolveDir = %q, want DefaultDir()", got)
	}
}
