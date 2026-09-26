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
	"os/exec"
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
// leading slash; "" is the root itself).
type fakeNode struct {
	kind    string // "file" or "dir"
	content string
}

// fakeTree is a small fixed repository:
//
//	README.md
//	trunk/main.go
func fakeTree() map[string]fakeNode {
	return map[string]fakeNode{
		"":              {kind: "dir"},
		"README.md":     {kind: "file", content: "hello world\n"},
		"trunk":         {kind: "dir"},
		"trunk/main.go": {kind: "file", content: "package main\n"},
	}
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
func newFakeServer() svn.Server {
	tree := fakeTree()
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

	var server svn.Server
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
	server.GetLatestRev = func() (int, error) { return 1, nil }
	server.Stat = func(path string, rev *uint) (svn.Dirent, error) {
		n, ok := tree[resolve(path)]
		if !ok {
			return svn.Dirent{}, fs.ErrNotExist
		}
		return svn.Dirent{
			Kind: n.kind, Size: uint64(len(n.content)),
			CreatedRev: 1, CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
		}, nil
	}
	// wirePath turns a tree key (bare, no leading slash; "" for the root)
	// into the full, slash-prefixed, repository-root-relative form
	// Server.List now requires of every Dirent.Path.
	wirePath := func(p string) string {
		return "/" + p
	}
	server.List = func(path string, rev *uint, depth string, fields, pattern []string) ([]svn.Dirent, error) {
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
			CreatedRev: 1, CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
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
				CreatedRev: 1, CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
			})
		}
		return out, nil
	}
	server.GetFile = func(path string, rev *uint, wantProps, wantContents bool) (uint, []svn.PropList, []byte, error) {
		n, ok := tree[resolve(path)]
		if !ok || n.kind != "file" {
			return 0, nil, nil, fs.ErrNotExist
		}
		if !wantContents {
			return 1, nil, nil, nil
		}
		return 1, nil, []byte(n.content), nil
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
		if !strings.Contains(out, "Revision: 1") {
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
