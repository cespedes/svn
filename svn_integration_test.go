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
// Layout after all three commits:
//
//	README.md       ("hello world\n" in r1, "hello world, v2\n" in r2)
//	trunk/main.go
//	trunk/sub/nested.txt
//	trunk/main_copy.go (copied from trunk/main.go in r3)
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

	run("copy", "-q", "trunk/main.go", "trunk/main_copy.go")
	run("commit", "-q", "-m", "copy main.go")

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
		if rev != 3 {
			t.Errorf("GetLatestRev() = %d, want 3", rev)
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
		if len(entries) != 3 {
			t.Fatalf("len(entries) = %d, want 3 (entries: %+v)", len(entries), entries)
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
		if byRev[3] != "copy main.go" {
			t.Errorf("r3 message = %q, want %q", byRev[3], "copy main.go")
		}
	})

	// The wire shape of a LogEntry.Changed entry (a fixed 4-element tuple:
	// path, mode, an optional copy-from group, an optional node-info
	// group) is exactly what a previous bug got wrong -- this checks
	// Client.Log actually decodes all of it correctly against a real
	// svnserve, including the copy-from info r3's commit has.
	t.Run("Log Changed includes copy-from info", func(t *testing.T) {
		three := 3
		entries, err := c.Log(nil, &three, &three, true)
		if err != nil {
			t.Fatalf("Log: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("len(entries) = %d, want 1 (entries: %+v)", len(entries), entries)
		}
		byPath := map[string]svn.ChangedPath{}
		for _, cp := range entries[0].Changed {
			byPath[cp.Path] = cp
		}
		cp, ok := byPath["/trunk/main_copy.go"]
		if !ok {
			t.Fatalf("Changed missing /trunk/main_copy.go (entries: %+v)", entries[0].Changed)
		}
		if cp.Mode != "A" {
			t.Errorf("Mode = %q, want %q", cp.Mode, "A")
		}
		if cp.Copy == nil {
			t.Fatalf("Copy is nil, want copy-from info")
		}
		if cp.Copy.Path != "/trunk/main.go" {
			t.Errorf("Copy.Path = %q, want %q", cp.Copy.Path, "/trunk/main.go")
		}
		if cp.Copy.Rev != 1 {
			t.Errorf("Copy.Rev = %d, want 1", cp.Copy.Rev)
		}
		if cp.Info == nil {
			t.Fatalf("Info is nil, want node info")
		}
		if cp.Info.NodeKind != "file" {
			t.Errorf("Info.NodeKind = %q, want %q", cp.Info.NodeKind, "file")
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
		if len(entries) != 4 {
			t.Fatalf("len(entries) = %d, want 4 (entries: %+v)", len(entries), entries)
		}
	})
}

// newDiskEditor returns an svn.Editor that writes every node it's told
// about as a real file/directory under destDir, exactly the kind of
// filesystem-backed implementation svn.Editor's own doc comment says
// belongs outside the library itself (see cmd/go-svn's own "checkout"/
// "update" subcommands for the real thing) -- built here, minimally, only
// to exercise Client.Checkout/Update's own wire behavior end to end
// against a real svnserve.
func newDiskEditor(t *testing.T, destDir string) svn.Editor {
	t.Helper()
	local := func(p string) string { return filepath.Join(destDir, filepath.FromSlash(p)) }
	return svn.Editor{
		AddDir: func(path string, copyFrom *svn.EditorCopyFrom) error {
			return os.MkdirAll(local(path), 0o755)
		},
		OpenDir: func(path string, rev int) error {
			return os.MkdirAll(local(path), 0o755)
		},
		DeleteEntry: func(path string, rev *int) error {
			return os.RemoveAll(local(path))
		},
		OpenFile: func(path string, rev int) ([]byte, error) {
			return os.ReadFile(local(path))
		},
		CloseFile: func(path string, content []byte) error {
			return os.WriteFile(local(path), content, 0o644)
		},
	}
}

// TestClientCheckout drives Client.Checkout against a real svnserve,
// which is what actually exercises the report/editor exchange it sends
// and the Editor Command Set sequence it then parses -- neither of which
// this package's own Server ever had to (it only ever produces that
// sequence, via EditorWriter, never consumes one), so a synthetic/
// in-memory server couldn't stand in for a real one here.
func TestClientCheckout(t *testing.T) {
	repoURL := newRealRepo(t)
	c, err := svn.Connect(repoURL)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	t.Run("plain checkout at the latest revision", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rev, err := c.Checkout(nil, newDiskEditor(t, dir))
		if err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		if rev != 3 {
			t.Errorf("Checkout() rev = %d, want 3", rev)
		}

		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("reading README.md: %v", err)
		}
		if string(readme) != "hello world, v2\n" {
			t.Errorf("README.md = %q, want %q", readme, "hello world, v2\n")
		}
		mainGo, err := os.ReadFile(filepath.Join(dir, "trunk", "main.go"))
		if err != nil {
			t.Fatalf("reading trunk/main.go: %v", err)
		}
		if string(mainGo) != "package main\n" {
			t.Errorf("trunk/main.go = %q, want %q", mainGo, "package main\n")
		}
		nested, err := os.ReadFile(filepath.Join(dir, "trunk", "sub", "nested.txt"))
		if err != nil {
			t.Fatalf("reading trunk/sub/nested.txt: %v", err)
		}
		if string(nested) != "nested\n" {
			t.Errorf("trunk/sub/nested.txt = %q, want %q", nested, "nested\n")
		}
		mainCopy, err := os.ReadFile(filepath.Join(dir, "trunk", "main_copy.go"))
		if err != nil {
			t.Fatalf("reading trunk/main_copy.go: %v", err)
		}
		if string(mainCopy) != "package main\n" {
			t.Errorf("trunk/main_copy.go = %q, want %q", mainCopy, "package main\n")
		}
	})

	t.Run("checkout at an older revision", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rev1 := 1
		rev, err := c.Checkout(&rev1, newDiskEditor(t, dir))
		if err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		if rev != 1 {
			t.Errorf("Checkout() rev = %d, want 1", rev)
		}

		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("reading README.md: %v", err)
		}
		if string(readme) != "hello world\n" {
			t.Errorf("README.md = %q, want %q (r1's own content, not r2's)", readme, "hello world\n")
		}
		// trunk/main_copy.go was only added in r3: it must not exist in a
		// checkout of r1.
		if _, err := os.Stat(filepath.Join(dir, "trunk", "main_copy.go")); err == nil {
			t.Errorf("trunk/main_copy.go should not exist in a checkout of r1")
		}
	})

	t.Run("checkout of a repository subdirectory", func(t *testing.T) {
		// A subdirectory checkout needs its own session anchored right
		// at "trunk" -- see Checkout's own doc comment for why it has
		// no separate "path within the repository" parameter.
		c2, err := svn.Connect(repoURL + "/trunk")
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		dir := filepath.Join(t.TempDir(), "wc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rev, err := c2.Checkout(nil, newDiskEditor(t, dir))
		if err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		if rev != 3 {
			t.Errorf("Checkout() rev = %d, want 3", rev)
		}
		mainGo, err := os.ReadFile(filepath.Join(dir, "main.go"))
		if err != nil {
			t.Fatalf("reading main.go: %v", err)
		}
		if string(mainGo) != "package main\n" {
			t.Errorf("main.go = %q, want %q", mainGo, "package main\n")
		}
		if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
			t.Errorf("README.md (a sibling of trunk, not under it) should not have been checked out")
		}
	})

	t.Run("Checkout after other commands on the same connection", func(t *testing.T) {
		// If an earlier subtest's Checkout left the connection desynced,
		// this would fail (or hang).
		if _, err := c.GetLatestRev(); err != nil {
			t.Fatalf("GetLatestRev after Checkout: %v", err)
		}
	})
}

// TestClientUpdate drives Client.Update against a real svnserve, checking
// out at r1 first (via Client.Checkout) and then updating in place --
// exercising every case newRealRepo's own r1-to-r3 history has: an
// unmodified file (trunk/main.go, trunk/sub/nested.txt), a modified one
// (README.md, changed again in r2), and a new one (trunk/main_copy.go,
// added in r3). A real svnserve serving an update (unlike this package's
// own Server) may send a modified file's content as a real incremental
// delta against the client's own reported base rather than a full
// replacement, which is what actually exercises applyEditor's "open-file"
// handling (reading the current on-disk content as decodeSvndiff's own
// source) rather than just its "add-file" one, already covered by
// TestClientCheckout.
func TestClientUpdate(t *testing.T) {
	repoURL := newRealRepo(t)
	c, err := svn.Connect(repoURL)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	t.Run("update from r1 to the latest revision", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		editor := newDiskEditor(t, dir)
		one := 1
		if _, err := c.Checkout(&one, editor); err != nil {
			t.Fatalf("Checkout: %v", err)
		}

		rev, err := c.Update(1, nil, editor)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if rev != 3 {
			t.Errorf("Update() rev = %d, want 3", rev)
		}

		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("reading README.md: %v", err)
		}
		if string(readme) != "hello world, v2\n" {
			t.Errorf("README.md = %q, want %q (r2's own content)", readme, "hello world, v2\n")
		}
		mainGo, err := os.ReadFile(filepath.Join(dir, "trunk", "main.go"))
		if err != nil {
			t.Fatalf("reading trunk/main.go: %v", err)
		}
		if string(mainGo) != "package main\n" {
			t.Errorf("trunk/main.go = %q, want %q (unchanged since r1)", mainGo, "package main\n")
		}
		nested, err := os.ReadFile(filepath.Join(dir, "trunk", "sub", "nested.txt"))
		if err != nil {
			t.Fatalf("reading trunk/sub/nested.txt: %v", err)
		}
		if string(nested) != "nested\n" {
			t.Errorf("trunk/sub/nested.txt = %q, want %q (unchanged since r1)", nested, "nested\n")
		}
		mainCopy, err := os.ReadFile(filepath.Join(dir, "trunk", "main_copy.go"))
		if err != nil {
			t.Fatalf("reading trunk/main_copy.go (added in r3): %v", err)
		}
		if string(mainCopy) != "package main\n" {
			t.Errorf("trunk/main_copy.go = %q, want %q", mainCopy, "package main\n")
		}
	})

	t.Run("update from r1 to r2 only", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		editor := newDiskEditor(t, dir)
		one := 1
		if _, err := c.Checkout(&one, editor); err != nil {
			t.Fatalf("Checkout: %v", err)
		}

		two := 2
		rev, err := c.Update(1, &two, editor)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if rev != 2 {
			t.Errorf("Update() rev = %d, want 2", rev)
		}

		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("reading README.md: %v", err)
		}
		if string(readme) != "hello world, v2\n" {
			t.Errorf("README.md = %q, want %q", readme, "hello world, v2\n")
		}
		// trunk/main_copy.go was only added in r3: it must not exist yet
		// after an update to r2.
		if _, err := os.Stat(filepath.Join(dir, "trunk", "main_copy.go")); err == nil {
			t.Errorf("trunk/main_copy.go should not exist after an update to r2")
		}
	})

	t.Run("no-op update already at the latest revision", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		editor := newDiskEditor(t, dir)
		if _, err := c.Checkout(nil, editor); err != nil {
			t.Fatalf("Checkout: %v", err)
		}
		rev, err := c.Update(3, nil, editor)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if rev != 3 {
			t.Errorf("Update() rev = %d, want 3", rev)
		}
	})

	t.Run("Update after other commands on the same connection", func(t *testing.T) {
		// If an earlier subtest's Update left the connection desynced,
		// this would fail (or hang).
		if _, err := c.GetLatestRev(); err != nil {
			t.Fatalf("GetLatestRev after Update: %v", err)
		}
	})
}

// TestClientDiff drives Client.Diff against a real svnserve, against a
// real working copy Client.Checkout produced (the same scenario
// Client.Update itself covers -- see Diff's own doc comment on why it's
// deliberately limited to comparing this Client's own connected
// location's history, not two different repository locations or a bare
// URL with no local content). Its own Editor reads Editor.OpenFile's
// "before" content from that working copy exactly like Client.Update's
// own would, but records Editor.CloseFile's "after" content instead of
// overwriting anything -- confirming Diff never mutates the working copy
// it's run against, unlike Update.
func TestClientDiff(t *testing.T) {
	repoURL := newRealRepo(t)
	c, err := svn.Connect(repoURL)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	t.Run("diff from r1 to the latest revision", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		one := 1
		if _, err := c.Checkout(&one, newDiskEditor(t, dir)); err != nil {
			t.Fatalf("Checkout: %v", err)
		}

		local := func(p string) string { return filepath.Join(dir, filepath.FromSlash(p)) }
		type change struct{ before, after string }
		changed := map[string]change{}
		var added, deleted []string
		editor := svn.Editor{
			AddDir: func(path string, copyFrom *svn.EditorCopyFrom) error { return nil },
			AddFile: func(path string, copyFrom *svn.EditorCopyFrom) error {
				added = append(added, path)
				return nil
			},
			DeleteEntry: func(path string, rev *int) error {
				deleted = append(deleted, path)
				return nil
			},
			OpenFile: func(path string, rev int) ([]byte, error) {
				return os.ReadFile(local(path))
			},
			CloseFile: func(path string, content []byte) error {
				before, _ := os.ReadFile(local(path)) // nil for a brand new (AddFile) path
				changed[path] = change{before: string(before), after: string(content)}
				return nil
			},
		}

		rev, err := c.Diff(1, nil, editor)
		if err != nil {
			t.Fatalf("Diff: %v", err)
		}
		if rev != 3 {
			t.Errorf("Diff() rev = %d, want 3", rev)
		}

		readme, ok := changed["README.md"]
		if !ok {
			t.Fatalf("README.md was not reported as changed")
		}
		if readme.before != "hello world\n" || readme.after != "hello world, v2\n" {
			t.Errorf("README.md change = %+v, want before %q, after %q", readme, "hello world\n", "hello world, v2\n")
		}
		if _, ok := changed["trunk/main.go"]; ok {
			t.Errorf("trunk/main.go (unchanged between r1 and r3) should never have been opened")
		}
		if len(added) != 1 || added[0] != "trunk/main_copy.go" {
			t.Errorf("added = %v, want just [trunk/main_copy.go]", added)
		}
		if got := changed["trunk/main_copy.go"].after; got != "package main\n" {
			t.Errorf("trunk/main_copy.go's new content = %q, want %q", got, "package main\n")
		}
		if len(deleted) != 0 {
			t.Errorf("deleted = %v, want none", deleted)
		}

		// Diff must not have touched the working copy Checkout produced:
		// it's still r1's own content, since nothing in this test's own
		// Editor ever wrote anything back to disk.
		readmeOnDisk, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("reading README.md: %v", err)
		}
		if string(readmeOnDisk) != "hello world\n" {
			t.Errorf("README.md on disk = %q, want %q (Diff must not modify the working copy)", readmeOnDisk, "hello world\n")
		}
		if _, err := os.Stat(filepath.Join(dir, "trunk", "main_copy.go")); err == nil {
			t.Errorf("trunk/main_copy.go should not have been created on disk by Diff")
		}
	})

	t.Run("Diff after other commands on the same connection", func(t *testing.T) {
		// If an earlier subtest's Diff left the connection desynced,
		// this would fail (or hang).
		if _, err := c.GetLatestRev(); err != nil {
			t.Fatalf("GetLatestRev after Diff: %v", err)
		}
	})
}
