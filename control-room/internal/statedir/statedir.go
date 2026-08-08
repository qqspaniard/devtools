// Package statedir resolves and secures Control Room's private local state
// directory and the files within it.
//
// The RFC requires a single private per-user state directory (mode 0700)
// holding the SQLite database, the control secret, and the broker socket, with
// sensitive files at mode 0600. This package centralizes path resolution
// (default per RFC, overridable via flag/env for tests and dogfood) and the
// permission discipline so no other package has to reason about it.
//
// It performs no networking and starts no processes; it only computes paths and
// enforces filesystem permissions.
package statedir

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvStateDir is the environment variable that overrides the default state
// directory. A CLI flag takes precedence over it; it takes precedence over the
// per-user default. Tests and dogfood runs set it to a temporary directory.
const EnvStateDir = "CONTROL_ROOM_STATE_DIR"

// File and directory names within the state directory. These are stable so a
// restarted broker finds the same files.
const (
	dbName     = "control-room.db"
	socketName = "broker.sock"
	secretName = "control.secret"
	logsName   = "logs"
)

// Permissions. The parent directory is private (0700); sensitive files are
// owner-only (0600).
const (
	DirPerm    os.FileMode = 0o700
	SecretPerm os.FileMode = 0o600
	DBPerm     os.FileMode = 0o600
)

// Paths holds the resolved absolute paths for one state directory.
type Paths struct {
	// Dir is the private state directory (mode 0700).
	Dir string
	// DB is the SQLite database file path (mode 0600).
	DB string
	// Socket is the Unix domain socket path. It lives inside Dir, so it is
	// reachable only through the private parent directory.
	Socket string
	// Secret is the control-secret file path (mode 0600).
	Secret string
	// Logs is the optional bounded log directory.
	Logs string
}

// Resolve computes the state paths. Precedence: explicit override (from a CLI
// flag) > CONTROL_ROOM_STATE_DIR env > per-user default. It does not touch the
// filesystem; call Ensure to create and verify the directory.
//
// The override argument is the value of a --state-dir flag, or "" if unset.
func Resolve(override string) (Paths, error) {
	dir := override
	if dir == "" {
		dir = os.Getenv(EnvStateDir)
	}
	if dir == "" {
		d, err := defaultStateDir()
		if err != nil {
			return Paths{}, err
		}
		dir = d
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Paths{}, fmt.Errorf("statedir: resolving %q: %w", dir, err)
	}
	return Paths{
		Dir:    abs,
		DB:     filepath.Join(abs, dbName),
		Socket: filepath.Join(abs, socketName),
		Secret: filepath.Join(abs, secretName),
		Logs:   filepath.Join(abs, logsName),
	}, nil
}

// defaultStateDir returns the per-user default state directory per the RFC:
// ~/.local/state/control-room on macOS and Linux. Following the RFC's stated
// default keeps the two OSes consistent; a platform-native path may replace it
// later.
func defaultStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("statedir: resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "control-room"), nil
}

// Ensure creates the state directory (mode 0700) if absent and verifies that an
// existing directory is safe to adopt: it must be a real directory (not a
// symlink), owned by the current user on Unix, and it is tightened to 0700 if
// more permissive.
//
// It fails closed if the path exists but is not a directory, is a symlink, or
// is owned by another user. This is defense in depth, NOT absolute same-user
// isolation: a process running as the current user can still reach this
// directory (see the RFC threat model). What it prevents is silently adopting a
// directory planted by, or owned by, someone else, or being redirected through
// a symlink.
//
// It does NOT create the database or secret; those are created by their owners
// (the store and the broker) with their own permission checks.
func (p Paths) Ensure() error {
	// lstat, not stat: we must detect a symlink at the path itself rather than
	// following it to whatever it targets.
	info, err := os.Lstat(p.Dir)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("statedir: %q is a symlink; refusing to adopt it (set a real --state-dir)", p.Dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("statedir: %q exists and is not a directory", p.Dir)
		}
		if err := verifyOwnership(p.Dir, info); err != nil {
			return err
		}
		// Tighten permissions if an existing directory is too open. This is a
		// best-effort defense; it cannot fix a hostile pre-existing directory
		// owned by another user (that case is already rejected above), but it
		// removes accidental group/other access.
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(p.Dir, DirPerm); err != nil {
				return fmt.Errorf("statedir: tightening permissions on %q: %w", p.Dir, err)
			}
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(p.Dir, DirPerm); err != nil {
			return fmt.Errorf("statedir: creating %q: %w", p.Dir, err)
		}
		// MkdirAll is subject to umask; force the exact private mode.
		if err := os.Chmod(p.Dir, DirPerm); err != nil {
			return fmt.Errorf("statedir: setting permissions on %q: %w", p.Dir, err)
		}
	default:
		return fmt.Errorf("statedir: stat %q: %w", p.Dir, err)
	}
	return nil
}
