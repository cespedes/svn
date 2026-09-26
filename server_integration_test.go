package svn_test

// Integration tests that run a real svn client against our own Server, the
// other direction from svn_integration_test.go (which runs our Client
// against a real svnserve). They skip cleanly if the real "svn" client
// isn't installed.

import (
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cespedes/svn"
)

// requireRealSVNClient skips the test unless the real "svn" client is on
// PATH. Unlike requireRealSVNTools (svn_integration_test.go), this test
// supplies its own Server, so svnadmin/svnserve aren't needed.
func requireRealSVNClient(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("svn"); err != nil {
		t.Skipf("svn not found in PATH; skipping test of Server against a real svn client")
	}
}

// runSVN runs the real svn client and returns its combined output.
func runSVN(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("svn", args...).CombinedOutput()
	return string(out), err
}

// fakeNode is one entry in the tiny, fixed, in-memory repository served by
// startFakeServer, keyed by its full path from the repository root (no
// leading slash; "" is the root itself). rev is the node's own
// CreatedRev, as of whichever fakeTreeAt snapshot it came from.
type fakeNode struct {
	kind    string // "file" or "dir"
	content string
	rev     int
}

// fakeLatestRev is the highest revision fakeTreeAt knows about, and what
// newFakeServer's GetLatestRev reports.
const fakeLatestRev = 2

// fakeTreeAt returns the repository's state as of rev (1 or 2; anything
// below 2 is treated as rev 1). At rev 1:
//
//	README.md
//	trunk/main.go
//	trunk/sub/nested.txt
//
// At rev 2 (fakeLatestRev): README.md is unchanged (same CreatedRev, so
// TestUpdate's real "svn update" subtest can confirm an unmodified file
// is never even opened); trunk/main.go's content changed; trunk/sub (and
// its only file) was removed; trunk/newdir/inside.txt and
// trunk/newfile.go were added -- exercising every UpdateEdit case
// (unchanged, modified, deleted, added) in one tree.
func fakeTreeAt(rev int) map[string]fakeNode {
	tree := map[string]fakeNode{
		"":              {kind: "dir", rev: 1},
		"README.md":     {kind: "file", rev: 1, content: "hello world\n"},
		"trunk":         {kind: "dir", rev: 1},
		"trunk/main.go": {kind: "file", rev: 1, content: "package main\n"},
	}
	if rev < 2 {
		tree["trunk/sub"] = fakeNode{kind: "dir", rev: 1}
		tree["trunk/sub/nested.txt"] = fakeNode{kind: "file", rev: 1, content: "nested\n"}
		return tree
	}
	tree["trunk"] = fakeNode{kind: "dir", rev: 2}
	tree["trunk/main.go"] = fakeNode{kind: "file", rev: 2, content: "package main\n\nfunc main() {}\n"}
	tree["trunk/newdir"] = fakeNode{kind: "dir", rev: 2}
	tree["trunk/newdir/inside.txt"] = fakeNode{kind: "file", rev: 2, content: "inside\n"}
	tree["trunk/newfile.go"] = fakeNode{kind: "file", rev: 2, content: "package main\n\nfunc New() {}\n"}
	return tree
}

// newFakeServer builds a fresh svn.Server backed by fakeTree, for exactly
// one connection. A real svn client connects with a URL that may point
// partway into the repository (e.g. ".../README.md" for a single-file
// "svn cat"), and then sends every subsequent command's path relative to
// that connect-time URL, not to the repository root -- so Greet records
// where the connect URL pointed (sessionBase), and every other callback
// resolves its path against it before looking it up in the tree.
//
// Building a new Server (and a new sessionBase closed over by it) per
// connection, rather than reusing one Server for all of them, is what
// keeps this resolution correct when multiple connections are served
// concurrently.
// newFakeServer returns a *svn.Server rather than a svn.Server: its own
// FinishReport closure calls back into server.CheckoutEdit/UpdateEdit,
// which read server.ReposInfo -- and that field is filled in later, by
// Serve's own handling of Greet, on whatever *Server Serve was actually
// called against. Returning a value here (and letting the caller call
// Serve on its own copy) would leave FinishReport reading a ReposInfo
// that Serve's mutation never reached, silently keeping it at its zero
// value.
func newFakeServer() *svn.Server {
	var sessionBase string

	resolve := func(path string) string {
		switch {
		case sessionBase == "":
			return path
		case path == "":
			return sessionBase
		default:
			return sessionBase + "/" + path
		}
	}
	// effectiveRev turns a callback's own rev argument (nil meaning
	// "latest") into a concrete revision number to key fakeTreeAt with.
	effectiveRev := func(rev *uint) int {
		if rev == nil {
			return fakeLatestRev
		}
		return int(*rev)
	}

	server := &svn.Server{}
	server.Greet = func(version int, capabilities []string, connectURL string, raclient string, client *string) (svn.ReposInfo, error) {
		u, err := url.Parse(connectURL)
		if err != nil {
			return svn.ReposInfo{}, err
		}
		sessionBase = strings.Trim(u.Path, "/")
		root := *u
		root.Path = "/"
		return svn.ReposInfo{
			UUID:         "9d3c8b6e-0000-0000-0000-000000000000",
			URL:          root.String(),
			Capabilities: []string{},
		}, nil
	}
	server.GetLatestRev = func() (int, error) { return fakeLatestRev, nil }
	var updateRev *uint
	var updateTarget string
	server.Update = func(rev *uint, target string, recurse bool) {
		updateRev = rev
		updateTarget = target
	}
	server.Diff = func(rev *uint, target string, recurse, ignoreAncestry bool, versusURL string, textDeltas bool, depth string) {
		// Reuses the same closure variables as Update: FinishReport
		// drives both "update" and "diff" through the exact same
		// UpdateEdit call, since the accumulated report looks identical
		// either way. Unlike Update's own target, a real client's "diff"
		// doesn't report target relative to whatever session happens to
		// be open (see svn.RepoRelativePath's own doc comment for why),
		// so the real target is derived from versusURL instead, then
		// stripped of this session's own anchor the same way every
		// other path argument already is.
		updateRev = rev
		if rel, err := svn.RepoRelativePath(server.ReposInfo.URL, versusURL); err == nil {
			updateTarget = strings.TrimPrefix(strings.TrimPrefix(rel, sessionBase), "/")
		} else {
			updateTarget = target
		}
	}
	server.Stat = func(path string, rev *uint) (svn.Dirent, error) {
		tree := fakeTreeAt(effectiveRev(rev))
		n, ok := tree[resolve(path)]
		if !ok {
			return svn.Dirent{}, fs.ErrNotExist
		}
		return svn.Dirent{
			Kind: n.kind, Size: uint64(len(n.content)),
			CreatedRev: uint(n.rev), CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
		}, nil
	}
	server.CheckPath = func(path string, rev *uint) (string, error) {
		tree := fakeTreeAt(effectiveRev(rev))
		n, ok := tree[resolve(path)]
		if !ok {
			return "none", nil
		}
		return n.kind, nil
	}
	// wirePath turns a tree key (bare, no leading slash; "" for the root)
	// into the full, slash-prefixed, repository-root-relative form
	// Server.List now requires of every Dirent.Path.
	wirePath := func(p string) string {
		return "/" + p
	}
	server.List = func(path string, rev *uint, depth string, fields, pattern []string) ([]svn.Dirent, error) {
		tree := fakeTreeAt(effectiveRev(rev))
		full := resolve(path)
		n, ok := tree[full]
		if !ok || n.kind != "dir" {
			return nil, fs.ErrNotExist
		}
		prefix := full
		if prefix != "" {
			prefix += "/"
		}
		// The queried directory itself is always included as one of the
		// entries, matching a real svnserve.
		out := []svn.Dirent{{
			Path: wirePath(full), Kind: "dir",
			CreatedRev: uint(n.rev), CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
		}}
		for p, e := range tree {
			if p == full || !strings.HasPrefix(p, prefix) {
				continue
			}
			rest := strings.TrimPrefix(p, prefix)
			if rest == "" || strings.Contains(rest, "/") {
				continue // not a direct child of full
			}
			out = append(out, svn.Dirent{
				Path: wirePath(p), Kind: e.kind, Size: uint64(len(e.content)),
				CreatedRev: uint(e.rev), CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
			})
		}
		return out, nil
	}
	server.GetFile = func(path string, rev *uint, wantProps, wantContents bool) (uint, []svn.PropList, []byte, error) {
		tree := fakeTreeAt(effectiveRev(rev))
		n, ok := tree[resolve(path)]
		if !ok || n.kind != "file" {
			return 0, nil, nil, fs.ErrNotExist
		}
		if !wantContents {
			return uint(n.rev), nil, nil, nil
		}
		return uint(n.rev), nil, []byte(n.content), nil
	}
	server.Log = func(paths []string, startRev, endRev uint, changedPaths bool) ([]svn.LogEntry, error) {
		entry := svn.LogEntry{
			Rev: 1, Author: "tester", Date: "2024-01-01T00:00:00.000000Z",
			Message: "initial commit",
		}
		if changedPaths {
			// trunk/main.go (a path containing '/', which a bare wire
			// word can't hold) as a plain modification, and a copy, to
			// exercise both the Path-as-string wire safety and the
			// optional Copy/Info groups.
			entry.Changed = []svn.ChangedPath{
				{
					Path: "/trunk/main.go", Mode: "M",
					Info: &svn.ChangedPathInfo{NodeKind: "file", TextMods: true, PropMods: false},
				},
				{
					Path: "/trunk/main_copy.go", Mode: "A",
					Copy: &svn.ChangedPathCopy{Path: "/trunk/main.go", Rev: 1},
					Info: &svn.ChangedPathInfo{NodeKind: "file", TextMods: false, PropMods: false},
				},
			}
		}
		return []svn.LogEntry{entry}, nil
	}
	server.FinishReport = func(report []svn.ReportedPath) ([]svn.Item, error) {
		toRev := updateRev
		if toRev == nil {
			latest := uint(fakeLatestRev)
			toRev = &latest
		}
		if svn.IsPlainCheckout(report) {
			return server.CheckoutEdit(report[0].Path, *toRev)
		}
		if fromRev, ok := svn.IsSingleRevisionUpdate(report); ok {
			return server.UpdateEdit(report[0].Path, updateTarget, fromRev, *toRev)
		}
		return nil, fmt.Errorf("fake server: unsupported report shape: %+v", report)
	}
	return server
}

// startFakeServer runs newFakeServer on a TCP listener bound to an
// OS-assigned free port on 127.0.0.1, speaking the protocol natively (the
// way `svnserve -d` would), and returns its "svn://host:port/" URL. The
// listener is closed when the test ends.
func startFakeServer(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed: test is done
			}
			go func() {
				defer conn.Close()
				server := newFakeServer()
				server.Serve(conn, conn)
			}()
		}
	}()

	return fmt.Sprintf("svn://%s/", ln.Addr().String())
}

func TestServerAgainstRealSVNClient(t *testing.T) {
	requireRealSVNClient(t)
	repoURL := startFakeServer(t)

	run := func(args ...string) string {
		t.Helper()
		args = append([]string{"--non-interactive"}, args...)
		out, err := runSVN(t, args...)
		if err != nil {
			t.Fatalf("svn %v: %v\n%s", args, err, out)
		}
		return out
	}

	t.Run("info", func(t *testing.T) {
		out := run("info", repoURL)
		if !strings.Contains(out, "Node Kind: directory") {
			t.Errorf("info output missing directory kind:\n%s", out)
		}
		if !strings.Contains(out, fmt.Sprintf("Revision: %d", fakeLatestRev)) {
			t.Errorf("info output missing revision:\n%s", out)
		}
		if !strings.Contains(out, "Last Changed Author: tester") {
			t.Errorf("info output missing author:\n%s", out)
		}
	})

	t.Run("ls", func(t *testing.T) {
		out := run("ls", repoURL)
		if !strings.Contains(out, "README.md") {
			t.Errorf("ls output missing README.md:\n%s", out)
		}
		if !strings.Contains(out, "trunk/") {
			t.Errorf("ls output missing trunk/:\n%s", out)
		}
	})

	// A session anchored below the repository root (connecting directly
	// to ".../trunk", as opposed to the root URL every other subtest
	// uses) sends "list" paths relative to that anchor, not to the
	// repository root -- this is what previously broke Server.List's
	// path reconstruction, since Serve had no way to know about the
	// anchor and rebuilt each entry's path as if "path" were already
	// repository-root-relative.
	t.Run("ls in a session anchored below the root", func(t *testing.T) {
		out := run("ls", repoURL+"trunk")
		if !strings.Contains(out, "main.go") {
			t.Errorf("ls output missing main.go:\n%s", out)
		}
	})

	t.Run("cat", func(t *testing.T) {
		out := run("cat", repoURL+"README.md")
		if out != "hello world\n" {
			t.Errorf("cat output = %q, want %q", out, "hello world\n")
		}
	})

	// A plain checkout: the client reports having nothing (a single
	// "set-path" for the root, start-empty), so Server.FinishReport (see
	// newFakeServer) answers via CheckoutEdit, which walks the whole tree
	// and describes every node as newly added. This is the first
	// end-to-end exercise of EditorWriter/CheckoutEdit against a real
	// client, not just a hand-checked Item sequence. Pinned to r1 (rather
	// than the fakeLatestRev state TestUpdate exercises) so this keeps
	// checking the plain, single-revision tree its own assertions below
	// describe.
	t.Run("checkout", func(t *testing.T) {
		dir := t.TempDir()
		run("checkout", "-r", "1", "-q", repoURL, dir)
		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("reading checked-out README.md: %v", err)
		}
		if string(readme) != "hello world\n" {
			t.Errorf("README.md content = %q, want %q", readme, "hello world\n")
		}
		mainGo, err := os.ReadFile(filepath.Join(dir, "trunk", "main.go"))
		if err != nil {
			t.Fatalf("reading checked-out trunk/main.go: %v", err)
		}
		if string(mainGo) != "package main\n" {
			t.Errorf("trunk/main.go content = %q, want %q", mainGo, "package main\n")
		}
		nested, err := os.ReadFile(filepath.Join(dir, "trunk", "sub", "nested.txt"))
		if err != nil {
			t.Fatalf("reading checked-out trunk/sub/nested.txt: %v", err)
		}
		if string(nested) != "nested\n" {
			t.Errorf("trunk/sub/nested.txt content = %q, want %q", nested, "nested\n")
		}
		// A real svn client's local metadata database asserts on a
		// missing svn:entry:committed-rev, aborting "checkout" outright
		// -- so getting this far already proves CheckoutEdit sent it (see
		// checkoutEmitEntryProps). This additionally confirms the values
		// came through correctly, by checking what "svn info" reports.
		info := run("info", dir)
		if !strings.Contains(info, "Revision: 1") {
			t.Errorf("info output missing revision:\n%s", info)
		}
		if !strings.Contains(info, "Last Changed Author: tester") {
			t.Errorf("info output missing author:\n%s", info)
		}
	})

	// A real "svn update" against a working copy already checked out at
	// r1: the client reports having r1 (not start-empty), so
	// Server.FinishReport answers via UpdateEdit instead of CheckoutEdit,
	// diffing r1 against fakeLatestRev. fakeTreeAt's r1-to-r2 change
	// exercises every case at once: README.md is untouched (must not
	// even be opened -- confirmed separately in checkout_test.go/
	// update_test.go, but a real client accepting the update at all is
	// itself partial evidence), trunk/main.go's content changed,
	// trunk/sub was removed, and trunk/newdir/inside.txt plus
	// trunk/newfile.go were added.
	t.Run("update", func(t *testing.T) {
		dir := t.TempDir()
		run("checkout", "-r", "1", "-q", repoURL, dir)
		run("update", "-q", dir)

		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatalf("reading README.md: %v", err)
		}
		if string(readme) != "hello world\n" {
			t.Errorf("README.md content = %q, want %q (unchanged)", readme, "hello world\n")
		}
		mainGo, err := os.ReadFile(filepath.Join(dir, "trunk", "main.go"))
		if err != nil {
			t.Fatalf("reading trunk/main.go: %v", err)
		}
		if want := "package main\n\nfunc main() {}\n"; string(mainGo) != want {
			t.Errorf("trunk/main.go content = %q, want %q", mainGo, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "trunk", "sub")); err == nil {
			t.Errorf("trunk/sub should have been removed by the update")
		}
		inside, err := os.ReadFile(filepath.Join(dir, "trunk", "newdir", "inside.txt"))
		if err != nil {
			t.Fatalf("reading trunk/newdir/inside.txt: %v", err)
		}
		if string(inside) != "inside\n" {
			t.Errorf("trunk/newdir/inside.txt content = %q, want %q", inside, "inside\n")
		}
		newfile, err := os.ReadFile(filepath.Join(dir, "trunk", "newfile.go"))
		if err != nil {
			t.Fatalf("reading trunk/newfile.go: %v", err)
		}
		if want := "package main\n\nfunc New() {}\n"; string(newfile) != want {
			t.Errorf("trunk/newfile.go content = %q, want %q", newfile, want)
		}

		info := run("info", dir)
		if !strings.Contains(info, fmt.Sprintf("Revision: %d", fakeLatestRev)) {
			t.Errorf("info output missing updated revision:\n%s", info)
		}

		status := run("status", dir)
		if strings.TrimSpace(status) != "" {
			t.Errorf("expected a clean status after update, got:\n%s", status)
		}
	})

	// "svn diff -r 1:2 URL": the same report/editor exchange as "update",
	// but starting from a "diff" command instead, which a real svnserve
	// acks differently on the wire (see newFakeServer's Diff and
	// TestServerDiffAcksFoldIntoFinishReport). No working copy involved
	// -- the client keeps rev 1's content in memory and computes the
	// diff itself from the same editor sequence UpdateEdit already
	// builds for "update".
	// "svn diff -r 1:2" against a real working copy: unlike diffing a
	// bare URL (which drives a second RA session of its own, fetching
	// the "old" side via "get-dir" -- a command this package doesn't
	// implement, since Server.Serve already advertises the "list"
	// capability that supersedes it for every other command that reads a
	// directory), diffing a working copy uses only the single report/
	// editor exchange already exercised by "update", since the "old"
	// side's content is already on disk locally.
	t.Run("diff", func(t *testing.T) {
		dir := t.TempDir()
		run("checkout", "-r", "1", "-q", repoURL, dir)
		out := run("diff", "-r", "1:2", dir)
		if !strings.Contains(out, "trunk/main.go") || !strings.Contains(out, "+func main() {}") {
			t.Errorf("diff output missing trunk/main.go's change:\n%s", out)
		}
		if !strings.Contains(out, "trunk/newfile.go") || !strings.Contains(out, "+func New() {}") {
			t.Errorf("diff output missing the added trunk/newfile.go:\n%s", out)
		}
		if !strings.Contains(out, "trunk/sub/nested.txt") || !strings.Contains(out, "-nested") {
			t.Errorf("diff output missing the deleted trunk/sub/nested.txt:\n%s", out)
		}
		if !strings.Contains(out, "trunk/newdir/inside.txt") || !strings.Contains(out, "+inside") {
			t.Errorf("diff output missing the added trunk/newdir/inside.txt:\n%s", out)
		}
		if strings.Contains(out, "README.md") {
			t.Errorf("diff output mentions README.md, which is unchanged between r1 and r2:\n%s", out)
		}
	})

	// "svn diff"/"svn update" of a single nested FILE (not a directory,
	// and not the whole working copy/repository): previously produced
	// silent empty output ("diff") or an outright "not a directory"
	// failure ("update"), since UpdateEdit/CheckoutEdit unconditionally
	// called List on the report path, which a file can never satisfy.
	// Both the working-copy form of "diff" (the server sees the report/
	// editor exchange run on the checkout's own already-anchored
	// session) and the bare-URL form (no working copy at all; the
	// server's session is anchored at the repository root, so the
	// target's true path is only recoverable via versusURL -- see
	// RepoRelativePath) are covered here, since each exercises a
	// different path through newFakeServer's Diff callback.
	t.Run("diff of a single nested file", func(t *testing.T) {
		dir := t.TempDir()
		run("checkout", "-r", "1", "-q", repoURL, dir)

		out := run("diff", "-r", "1:2", filepath.Join(dir, "trunk", "main.go"))
		if !strings.Contains(out, "trunk/main.go") || !strings.Contains(out, "+func main() {}") {
			t.Errorf("diff output missing trunk/main.go's change:\n%s", out)
		}
		if strings.Contains(out, "newfile.go") || strings.Contains(out, "README.md") {
			t.Errorf("diff output should only cover trunk/main.go, got:\n%s", out)
		}

		out = run("diff", "-r", "1:2", repoURL+"trunk/main.go")
		if !strings.Contains(out, "main.go") || !strings.Contains(out, "+func main() {}") {
			t.Errorf("URL-form diff output missing main.go's change:\n%s", out)
		}
	})

	t.Run("update of a single nested file", func(t *testing.T) {
		dir := t.TempDir()
		run("checkout", "-r", "1", "-q", repoURL, dir)
		run("update", "-q", filepath.Join(dir, "trunk", "main.go"))

		mainGo, err := os.ReadFile(filepath.Join(dir, "trunk", "main.go"))
		if err != nil {
			t.Fatalf("reading trunk/main.go: %v", err)
		}
		if want := "package main\n\nfunc main() {}\n"; string(mainGo) != want {
			t.Errorf("trunk/main.go content = %q, want %q", mainGo, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "trunk", "newfile.go")); err == nil {
			t.Errorf("trunk/newfile.go should not have been created by updating just trunk/main.go")
		}
	})

	// "svn update" of a single nested DIRECTORY (as opposed to the file
	// case above): the target itself is a directory, so UpdateEdit must
	// both resend its entry-props (skipped for path segments strictly
	// above the target -- see UpdateEdit's own doc comment) and recurse
	// into its own children normally, exercising the directory branch of
	// updateNavigateToTarget end to end.
	t.Run("update of a single nested directory", func(t *testing.T) {
		dir := t.TempDir()
		run("checkout", "-r", "1", "-q", repoURL, dir)
		run("update", "-q", filepath.Join(dir, "trunk"))

		mainGo, err := os.ReadFile(filepath.Join(dir, "trunk", "main.go"))
		if err != nil {
			t.Fatalf("reading trunk/main.go: %v", err)
		}
		if want := "package main\n\nfunc main() {}\n"; string(mainGo) != want {
			t.Errorf("trunk/main.go content = %q, want %q", mainGo, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "trunk", "sub")); err == nil {
			t.Errorf("trunk/sub should have been removed by the update")
		}
		newfile, err := os.ReadFile(filepath.Join(dir, "trunk", "newfile.go"))
		if err != nil {
			t.Fatalf("reading trunk/newfile.go: %v", err)
		}
		if want := "package main\n\nfunc New() {}\n"; string(newfile) != want {
			t.Errorf("trunk/newfile.go content = %q, want %q", newfile, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
			t.Errorf("README.md should still be there, untouched by updating just trunk: %v", err)
		}
	})

	// A checkout/update of a repository SUBDIRECTORY (as opposed to every
	// other checkout/update subtest here, which connects at the
	// repository root): this exercises two things neither of the above
	// does. First, "get-iprops" -- a real client sends it as part of any
	// checkout below the repository root, and previously got "E210001:
	// Unknown command 'get-iprops'" outright, since it had no case in
	// Serve's switch at all. Second, a real anchored session: fakeTree's
	// own Dirent.Path values (built by resolve()+wirePath()) are always
	// repository-root-relative, e.g. "/trunk/main.go", never anchor-
	// relative -- confirmed against a real svnserve to always be this
	// way -- so CheckoutEdit/UpdateEdit's own self-entry/child-name
	// bookkeeping must not assume it can find the self-entry at
	// "/" + whatever anchor-relative path was queried with (which is
	// only ever "" for the report's own root): before both were fixed,
	// this failed with "E210004: Malformed network data".
	t.Run("checkout and update of a repository subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		run("checkout", "-r", "1", "-q", repoURL+"trunk", dir)
		mainGo, err := os.ReadFile(filepath.Join(dir, "main.go"))
		if err != nil {
			t.Fatalf("reading main.go: %v", err)
		}
		if string(mainGo) != "package main\n" {
			t.Errorf("main.go content = %q, want %q", mainGo, "package main\n")
		}
		nested, err := os.ReadFile(filepath.Join(dir, "sub", "nested.txt"))
		if err != nil {
			t.Fatalf("reading sub/nested.txt: %v", err)
		}
		if string(nested) != "nested\n" {
			t.Errorf("sub/nested.txt content = %q, want %q", nested, "nested\n")
		}

		run("update", "-q", dir)
		mainGo, err = os.ReadFile(filepath.Join(dir, "main.go"))
		if err != nil {
			t.Fatalf("reading updated main.go: %v", err)
		}
		if want := "package main\n\nfunc main() {}\n"; string(mainGo) != want {
			t.Errorf("updated main.go content = %q, want %q", mainGo, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "sub")); err == nil {
			t.Errorf("sub should have been removed by the update")
		}
		inside, err := os.ReadFile(filepath.Join(dir, "newdir", "inside.txt"))
		if err != nil {
			t.Fatalf("reading newdir/inside.txt: %v", err)
		}
		if string(inside) != "inside\n" {
			t.Errorf("newdir/inside.txt content = %q, want %q", inside, "inside\n")
		}
	})

	t.Run("log", func(t *testing.T) {
		out := run("log", repoURL)
		if !strings.Contains(out, "initial commit") {
			t.Errorf("log output missing commit message:\n%s", out)
		}
		if !strings.Contains(out, "tester") {
			t.Errorf("log output missing author:\n%s", out)
		}
	})

	// A LogEntry.Changed path is sent as a plain Go string; if server.go
	// ever marshaled that as a bare wire word instead of a length-prefixed
	// string, a path containing '/' (i.e. almost every real path) would
	// produce invalid wire syntax. Also checks that copy-from info comes
	// through correctly, since "svn log -v" prints it.
	t.Run("log -v shows changed paths, including a copy", func(t *testing.T) {
		out := run("log", "-v", repoURL)
		if !strings.Contains(out, "/trunk/main.go") {
			t.Errorf("log -v output missing the modified path:\n%s", out)
		}
		if !strings.Contains(out, "/trunk/main_copy.go") {
			t.Errorf("log -v output missing the added path:\n%s", out)
		}
		if !strings.Contains(out, "(from /trunk/main.go:1)") {
			t.Errorf("log -v output missing copy-from info:\n%s", out)
		}
	})

	// A real svn client rejects a "stat" response shaped as a zero-element
	// tuple (the outer "( )") with "E210004: Malformed network data",
	// instead of reporting the path as missing: the tuple must always
	// have exactly one slot, itself holding an empty list when the entry
	// is absent. Confirmed by temporarily reverting server.go's "stat" 404
	// case to the zero-element shape and seeing this exact error appear.
	t.Run("info on nonexistent path", func(t *testing.T) {
		out, err := runSVN(t, "--non-interactive", "info", repoURL+"does-not-exist")
		if err == nil {
			t.Fatalf("expected an error, got none; output:\n%s", out)
		}
		if strings.Contains(out, "Malformed network data") {
			t.Fatalf("real svn client rejected the response as malformed:\n%s", out)
		}
		if !strings.Contains(out, "non-existent") {
			t.Errorf("expected a \"non-existent\" error, got:\n%s", out)
		}
	})
}
