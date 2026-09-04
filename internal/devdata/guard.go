package devdata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrDatabaseExists reports a seed run that would write over a database
	// somebody may have been testing against.
	ErrDatabaseExists = errors.New("a database already exists at the target path")
	// ErrOutsideWorkingDir reports a wipe aimed outside the working directory,
	// which is where a shared or real data dir would be.
	ErrOutsideWorkingDir = errors.New("refusing to wipe a database outside the working directory")
	// ErrEnvFileExists reports a destination this run must not clobber.
	ErrEnvFileExists = errors.New("env file already exists")
)

// CheckEnvWritable reports whether the run may write path later. It is checked
// up front because the stub endpoints it would carry only exist while the
// process does, so failing after binding them loses them.
func CheckEnvWritable(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrEnvFileExists, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return nil
}

// CheckTarget decides whether a seed run may proceed. It keys on the resolved
// path because the dangerous case is a data dir pointing somewhere shared.
func CheckTarget(dbPath, workingDir string, reset, force bool) error {
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", dbPath, err)
	}
	if !reset {
		return fmt.Errorf("%w: %s (pass --reset to wipe and reseed it)", ErrDatabaseExists, dbPath)
	}
	inside, err := within(dbPath, workingDir)
	if err != nil {
		return err
	}
	if !inside && !force {
		return fmt.Errorf("%w: %s (pass --force if you meant it)", ErrOutsideWorkingDir, dbPath)
	}
	return nil
}

// within resolves both ends before comparing, since a data dir reached through a
// symlink is an ordinary NAS shape and would otherwise read as outside.
func within(path, dir string) (bool, error) {
	absDir, err := resolve(dir)
	if err != nil {
		return false, err
	}
	absPath, err := resolve(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false, nil
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

func resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}
