package svnfs_test

// Integration tests against a real svnadmin/svn/svnserve, if installed.
// They skip cleanly (t.Skip) when those tools aren't on PATH, so plain
// `go test ./...` still works on a machine without SVN installed.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/cespedes/svn"
	"github.com/cespedes/svn/svnfs"
)

// requireRealSVNTools skips the test unless svnadmin, svn and svnserve are
// all available on PATH. svnserve isn't invoked directly here, but
// svn.Connect's "file://" handling execs it under the hood.
func requireRealSVNTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"svnadmin", "svn", "svnserve"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found in PATH; skipping test against a real svnserve", tool)
		}
	}
}

// newRealRepo creates a temporary SVN repository with a real svnadmin,
// populates it with a real svn client, and returns its file:// URL. It
// skips the test if the required tools aren't installed.
//
// Layout:
//
//	README.md
//	trunk/main.go
//	trunk/sub/nested.txt
func newRealRepo(t *testing.T) string {
	t.Helper()
	requireRealSVNTools(t)

	repoPath := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("svnadmin", "create", repoPath).CombinedOutput(); err != nil {
		t.Fatalf("svnadmin create: %v\n%s", err, out)
	}
	repoURL := "file://" + repoPath

	wc := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		args = append([]string{"--non-interactive"}, args...)
		cmd := exec.Command("svn", args...)
		cmd.Dir = wc
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("svn %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(wc, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("checkout", "-q", repoURL, ".")
	write("README.md", "hello world\n")
	write("trunk/main.go", "package main\n")
	write("trunk/sub/nested.txt", "nested\n")
	run("add", "-q", "README.md", "trunk")
	run("commit", "-q", "-m", "initial commit")

	return repoURL
}

func TestFSAgainstRealSVNServer(t *testing.T) {
	repoURL := newRealRepo(t)

	c, err := svn.Connect(repoURL)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	fsys := svnfs.New(c, nil)

	if err := fstest.TestFS(fsys,
		"README.md",
		"trunk/main.go",
		"trunk/sub/nested.txt",
	); err != nil {
		t.Fatal(err)
	}

	t.Run("directory Size is not svnserve's invalid-size sentinel", func(t *testing.T) {
		info, err := fsys.Stat("trunk")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Size() != 0 {
			t.Errorf("Stat(trunk).Size() = %d, want 0", info.Size())
		}
	})

	t.Run("ReadDir entries are bare child names", func(t *testing.T) {
		entries, err := fsys.ReadDir("trunk")
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		names := map[string]bool{}
		for _, e := range entries {
			names[e.Name()] = true
		}
		for _, want := range []string{"main.go", "sub"} {
			if !names[want] {
				t.Errorf("ReadDir(trunk) missing entry %q, got %v", want, names)
			}
		}
	})

	t.Run("ReadFile", func(t *testing.T) {
		content, err := fsys.ReadFile("README.md")
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(content) != "hello world\n" {
			t.Errorf("content = %q, want %q", content, "hello world\n")
		}
	})
}
