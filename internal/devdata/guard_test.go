package devdata

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

func TestFreshDatabaseNeedsNoFlags(t *testing.T) {
	wd := t.TempDir()
	if err := CheckTarget(filepath.Join(wd, "data", "transpondarr.db"), wd, false, false); err != nil {
		t.Errorf("CheckTarget on a fresh path = %v, want nil", err)
	}
}

func TestSeedRefusesExistingDatabaseWithoutReset(t *testing.T) {
	wd := t.TempDir()
	db := filepath.Join(wd, "transpondarr.db")
	touch(t, db)

	err := CheckTarget(db, wd, false, false)
	if !errors.Is(err, ErrDatabaseExists) {
		t.Errorf("CheckTarget = %v, want ErrDatabaseExists", err)
	}
}

func TestResetInsideWorkingDirectoryNeedsNoForce(t *testing.T) {
	wd := t.TempDir()
	db := filepath.Join(wd, "data", "transpondarr.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, db)

	if err := CheckTarget(db, wd, true, false); err != nil {
		t.Errorf("CheckTarget with --reset inside the working dir = %v, want nil", err)
	}
}

func TestResetOutsideWorkingDirectoryRequiresForce(t *testing.T) {
	wd := t.TempDir()
	elsewhere := t.TempDir()
	db := filepath.Join(elsewhere, "transpondarr.db")
	touch(t, db)

	err := CheckTarget(db, wd, true, false)
	if !errors.Is(err, ErrOutsideWorkingDir) {
		t.Errorf("CheckTarget = %v, want ErrOutsideWorkingDir", err)
	}
	if err := CheckTarget(db, wd, true, true); err != nil {
		t.Errorf("CheckTarget with --force = %v, want nil", err)
	}
}

// A path outside the working directory is only a problem when something is
// there to destroy: the guard is about the wipe, not about where you seed.
func TestOutsideWorkingDirectoryIsFineWhenNothingIsThere(t *testing.T) {
	wd := t.TempDir()
	elsewhere := t.TempDir()

	if err := CheckTarget(filepath.Join(elsewhere, "transpondarr.db"), wd, true, false); err != nil {
		t.Errorf("CheckTarget on a fresh outside path = %v, want nil", err)
	}
}

func TestEnvFileIsCheckedBeforeAnythingIsWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	if err := CheckEnvWritable(path); err != nil {
		t.Errorf("CheckEnvWritable on a free path = %v, want nil", err)
	}
	touch(t, path)
	if err := CheckEnvWritable(path); !errors.Is(err, ErrEnvFileExists) {
		t.Errorf("CheckEnvWritable = %v, want ErrEnvFileExists", err)
	}
}
