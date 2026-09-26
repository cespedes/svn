package main

// Integration tests that run go-svn's own subcommands against a real,
// temporary svnadmin-created repository (exec'ing a real svnserve under
// the hood, via svn.Connect's "file://" handling). They skip cleanly if
// svnadmin/svn/svnserve aren't on PATH.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireRealSVNTools skips the test unless svnadmin, svn and svnserve
// are all available on PATH.
func requireRealSVNTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"svnadmin", "svn", "svnserve"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found in PATH; skipping test against a real svnserve", tool)
		}
	}
}

// newExportTestRepo creates a temporary repository with a real svnadmin,
// populates it with a nested tree over one commit, and returns its
// file:// URL. Layout:
//
//	README.md
//	trunk/main.go
//	trunk/sub/nested.txt
func newExportTestRepo(t *testing.T) string {
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}

// TestExport checks "go-svn export" against a real svnserve: a plain
// export of the whole repository, an export anchored below the
// repository root (which -- confirmed against a real svnserve -- returns
// every Dirent.Path relative to the repository root regardless of the
// session's own anchor, unlike every List/GetFile path argument the
// caller sends, which stays anchor-relative; see repoRootRelativePath),
// and a single-file export.
func TestExport(t *testing.T) {
	repoURL := newExportTestRepo(t)
	tmp := t.TempDir()

	t.Run("whole repository", func(t *testing.T) {
		dest := filepath.Join(tmp, "root")
		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL, dest}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, filepath.Join(dest, "README.md")); got != "hello world\n" {
			t.Errorf("README.md = %q, want %q", got, "hello world\n")
		}
		if got := readFile(t, filepath.Join(dest, "trunk", "main.go")); got != "package main\n" {
			t.Errorf("trunk/main.go = %q, want %q", got, "package main\n")
		}
		if got := readFile(t, filepath.Join(dest, "trunk", "sub", "nested.txt")); got != "nested\n" {
			t.Errorf("trunk/sub/nested.txt = %q, want %q", got, "nested\n")
		}
		if _, err := os.Stat(filepath.Join(dest, ".svn")); err == nil {
			t.Errorf("export left a .svn directory behind")
		}
	})

	t.Run("anchored below the repository root", func(t *testing.T) {
		dest := filepath.Join(tmp, "trunk-only")
		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL + "/trunk", dest}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, filepath.Join(dest, "main.go")); got != "package main\n" {
			t.Errorf("main.go = %q, want %q", got, "package main\n")
		}
		if got := readFile(t, filepath.Join(dest, "sub", "nested.txt")); got != "nested\n" {
			t.Errorf("sub/nested.txt = %q, want %q", got, "nested\n")
		}
		if _, err := os.Stat(filepath.Join(dest, "trunk")); err == nil {
			t.Fatalf("export created a spurious nested %q directory (the self-entry was mistaken for a child)", filepath.Join(dest, "trunk"))
		}
	})

	t.Run("single file", func(t *testing.T) {
		dest := filepath.Join(tmp, "readme-only.txt")
		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL + "/README.md", dest}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, dest); got != "hello world\n" {
			t.Errorf("content = %q, want %q", got, "hello world\n")
		}
	})

	t.Run("default destination derived from the URL", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)

		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL + "/trunk"}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, filepath.Join(dir, "trunk", "main.go")); got != "package main\n" {
			t.Errorf("trunk/main.go = %q, want %q", got, "package main\n")
		}
	})
}
