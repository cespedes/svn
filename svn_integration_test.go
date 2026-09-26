package svn_test

// Integration tests against a real svnadmin/svn/svnserve, if installed.
// They skip cleanly (t.Skip) when those tools aren't on PATH, so plain
// `go test ./...` still works on a machine without SVN installed.

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"

	"github.com/cespedes/svn"
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
// populates it over two commits with a real svn client, and returns its
// file:// URL. It skips the test if the required tools aren't installed.
//
// Layout after both commits:
//
//	README.md       ("hello world\n" in r1, "hello world, v2\n" in r2)
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

	write("README.md", "hello world, v2\n")
	run("commit", "-q", "-m", "update README")

	return repoURL
}

func TestClientAgainstRealSVNServer(t *testing.T) {
	repoURL := newRealRepo(t)

	c, err := svn.Connect(repoURL)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	t.Run("GetLatestRev", func(t *testing.T) {
		rev, err := c.GetLatestRev()
		if err != nil {
			t.Fatalf("GetLatestRev: %v", err)
		}
		if rev != 2 {
			t.Errorf("GetLatestRev() = %d, want 2", rev)
		}
	})

	t.Run("Stat root", func(t *testing.T) {
		stat, err := c.Stat("", nil)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if stat.Kind != "dir" {
			t.Errorf("Stat(\"\").Kind = %q, want %q", stat.Kind, "dir")
		}
	})

	t.Run("Stat file at latest revision", func(t *testing.T) {
		stat, err := c.Stat("README.md", nil)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if stat.Kind != "file" {
			t.Errorf("Kind = %q, want %q", stat.Kind, "file")
		}
		if stat.CreatedRev != 2 {
			t.Errorf("CreatedRev = %d, want 2", stat.CreatedRev)
		}
		if stat.LastAuthor == "" {
			t.Errorf("LastAuthor is empty")
		}
		if stat.CreatedDate == "" {
			t.Errorf("CreatedDate is empty")
		}
	})

	t.Run("Stat file at an older revision", func(t *testing.T) {
		rev1 := 1
		stat, err := c.Stat("README.md", &rev1)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if stat.CreatedRev != 1 {
			t.Errorf("CreatedRev = %d, want 1", stat.CreatedRev)
		}
	})

	t.Run("Stat nonexistent path", func(t *testing.T) {
		_, err := c.Stat("does-not-exist", nil)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("error = %v, want errors.Is(err, fs.ErrNotExist)", err)
		}
	})

	t.Run("List", func(t *testing.T) {
		entries, err := c.List("trunk", nil, "immediates",
			[]string{"kind", "size", "created-rev", "time", "last-author"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		kinds := map[string]string{}
		for _, e := range entries {
			// e.Path may come back relative to the repo root rather than
			// to "trunk" (see svnfs.readDir's comment on this); take the
			// bare basename either way.
			kinds[path.Base(e.Path)] = e.Kind
		}
		if kinds["main.go"] != "file" {
			t.Errorf("main.go kind = %q, want %q (entries: %+v)", kinds["main.go"], "file", entries)
		}
		if kinds["sub"] != "dir" {
			t.Errorf("sub kind = %q, want %q (entries: %+v)", kinds["sub"], "dir", entries)
		}
	})

	t.Run("GetFile then GetLatestRev on the same connection", func(t *testing.T) {
		_, content, err := c.GetFile("README.md", nil, false, true)
		if err != nil {
			t.Fatalf("GetFile: %v", err)
		}
		if string(content) != "hello world, v2\n" {
			t.Errorf("content = %q, want %q", content, "hello world, v2\n")
		}
		// If GetFile left the connection desynced, this would fail (or,
		// against some servers, hang).
		if _, err := c.GetLatestRev(); err != nil {
			t.Fatalf("GetLatestRev after GetFile: %v", err)
		}
	})

	t.Run("Log with an explicit end revision", func(t *testing.T) {
		one := 1
		entries, err := c.Log(nil, nil, &one, true)
		if err != nil {
			t.Fatalf("Log: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("len(entries) = %d, want 2 (entries: %+v)", len(entries), entries)
		}
		byRev := map[uint]string{}
		for _, e := range entries {
			byRev[e.Rev] = e.Message
		}
		if byRev[1] != "initial commit" {
			t.Errorf("r1 message = %q, want %q", byRev[1], "initial commit")
		}
		if byRev[2] != "update README" {
			t.Errorf("r2 message = %q, want %q", byRev[2], "update README")
		}
	})

	t.Run("Log with no bounds includes revision 0", func(t *testing.T) {
		// A nil startRev and nil endRev both default to their documented
		// meaning ("latest" and "0" respectively), so this asks for the
		// full history -- which, for a real repository, includes r0
		// itself (the empty state svnadmin create leaves behind).
		entries, err := c.Log(nil, nil, nil, true)
		if err != nil {
			t.Fatalf("Log: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("len(entries) = %d, want 3 (entries: %+v)", len(entries), entries)
		}
	})
}
