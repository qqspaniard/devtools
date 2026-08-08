package statedir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureCreatesPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	p, err := Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	info, err := os.Stat(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("expected a directory")
	}
	if perm := info.Mode().Perm(); perm != DirPerm {
		t.Fatalf("dir perm = %o, want %o", perm, DirPerm)
	}
}

func TestEnsureAdoptsExistingOwnedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The dir is owned by us (we just made it); Ensure must accept it.
	if err := p.Ensure(); err != nil {
		t.Fatalf("Ensure on owned dir: %v", err)
	}
}

func TestEnsureTightensLoosePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, _ := Resolve(dir)
	if err := p.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	info, _ := os.Stat(dir)
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("expected group/other bits cleared, got %o", perm)
	}
}

func TestEnsureRejectsSymlinkDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	p, _ := Resolve(link)
	if err := p.Ensure(); err == nil {
		t.Fatal("expected Ensure to reject a symlink state dir")
	}
}

func TestEnsureRejectsNonDirectory(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := Resolve(file)
	if err := p.Ensure(); err == nil {
		t.Fatal("expected Ensure to reject a non-directory path")
	}
}

func TestEnsureRejectsForeignOwnedDir(t *testing.T) {
	// We cannot chown to another uid without privilege. Instead, exercise the
	// ownership predicate against a directory that already belongs to another
	// user: on Unix, /usr is root-owned and any non-root test process differs
	// from it. This proves Ensure fails closed on a foreign-owned directory.
	// Unix-only (see ownership_unix.go); skipped when running as root.
	if runtime.GOOS == "windows" {
		t.Skip("no uid ownership on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root; ownership check would pass for any dir")
	}
	// First, our own fresh dir must pass the predicate.
	own := t.TempDir()
	if info, err := os.Lstat(own); err != nil {
		t.Fatal(err)
	} else if err := verifyOwnership(own, info); err != nil {
		t.Fatalf("verifyOwnership on own dir: %v", err)
	}

	// A root-owned system directory must be rejected by Ensure's ownership gate.
	for _, foreign := range []string{"/usr", "/var", "/etc"} {
		info, err := os.Lstat(foreign)
		if err != nil || !info.IsDir() {
			continue
		}
		p, _ := Resolve(foreign)
		// Ensure adopts an existing dir only if owned by us; a root-owned dir
		// must be refused.
		if err := p.Ensure(); err == nil {
			t.Fatalf("expected Ensure to reject foreign-owned %q", foreign)
		}
		return // one foreign dir is enough
	}
	t.Skip("no foreign-owned system directory available to test")
}
