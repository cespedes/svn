package svnfs_test

import (
	"errors"
	"io"
	"io/fs"
	"math"
	"net"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cespedes/svn"
	"github.com/cespedes/svn/svnfs"
)

// svnSize mimics a real svnserve, which reports a directory's "size" as
// SVN_INVALID_FILESIZE -- all bits set, i.e. math.MaxUint64 -- rather than
// leaving it at some small, meaningless number like the byte length of
// this fake tree's own (empty, for directories) content field.
func svnSize(n *memNode) uint64 {
	if n.kind == "dir" {
		return math.MaxUint64
	}
	return uint64(len(n.content))
}

// memNode is a tiny in-memory repository tree, used to back a real
// svn.Server for these tests.
type memNode struct {
	kind     string // "file" or "dir"
	content  []byte
	rev      uint
	date     string
	author   string
	children map[string]*memNode
}

func (n *memNode) lookup(path string) *memNode {
	cur := n
	if path == "" {
		return cur
	}
	for _, part := range strings.Split(path, "/") {
		if cur.children == nil {
			return nil
		}
		next, ok := cur.children[part]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func joinSVNPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func buildTree() *memNode {
	return &memNode{
		kind: "dir", rev: 10, date: "2024-01-01T00:00:00.000000Z", author: "root",
		children: map[string]*memNode{
			"README.md": {
				kind: "file", content: []byte("hello\n"),
				rev: 5, date: "2023-01-01T00:00:00.000000Z", author: "alice",
			},
			"trunk": {
				kind: "dir", rev: 8, date: "2023-06-01T00:00:00.000000Z", author: "bob",
				children: map[string]*memNode{
					"main.go": {
						kind: "file", content: []byte("package main\n"),
						rev: 9, date: "2023-07-01T00:00:00.000000Z", author: "carol",
					},
					"sub": {
						kind: "dir", rev: 7, date: "2023-05-01T00:00:00.000000Z", author: "dave",
						children: map[string]*memNode{
							"nested.txt": {
								kind: "file", content: []byte("nested\n"),
								rev: 6, date: "2023-04-01T00:00:00.000000Z", author: "erin",
							},
						},
					},
				},
			},
		},
	}
}

// newTestFS wires an svnfs.FS to a real svn.Server (backed by an in-memory
// tree) over an in-memory duplex connection, and cleans everything up when
// the test ends.
func newTestFS(t *testing.T) *svnfs.FS {
	t.Helper()
	tree := buildTree()

	clientSide, serverSide := net.Pipe()

	var server svn.Server
	server.Stat = func(path string, rev *uint) (svn.Dirent, error) {
		n := tree.lookup(path)
		if n == nil {
			return svn.Dirent{}, fs.ErrNotExist
		}
		return svn.Dirent{
			Kind: n.kind, Size: svnSize(n),
			CreatedRev: n.rev, CreatedDate: n.date, LastAuthor: n.author,
		}, nil
	}
	server.List = func(path string, rev *uint, depth string, fields, pattern []string) ([]svn.Dirent, error) {
		n := tree.lookup(path)
		if n == nil || n.kind != "dir" {
			return nil, errors.New("not found")
		}
		// A real svnserve returns each Path prefixed with "/" + the full
		// queried path (not a bare child name), and includes the queried
		// directory itself as one of its own "children" -- confirmed
		// against a real server; svnfs.readDir must cope with both.
		out := []svn.Dirent{
			{Path: "/" + path, Kind: n.kind, Size: svnSize(n),
				CreatedRev: n.rev, CreatedDate: n.date, LastAuthor: n.author},
		}
		for name, c := range n.children {
			out = append(out, svn.Dirent{
				Path: "/" + joinSVNPath(path, name),
				Kind: c.kind, Size: svnSize(c),
				CreatedRev: c.rev, CreatedDate: c.date, LastAuthor: c.author,
			})
		}
		return out, nil
	}
	server.GetFile = func(path string, rev *uint, wantProps, wantContents bool) (uint, []svn.PropList, []byte, error) {
		n := tree.lookup(path)
		if n == nil || n.kind != "file" {
			return 0, nil, nil, errors.New("not found")
		}
		if !wantContents {
			return n.rev, nil, nil, nil
		}
		return n.rev, nil, n.content, nil
	}

	done := make(chan error, 1)
	go func() { done <- server.Serve(serverSide, serverSide) }()
	t.Cleanup(func() {
		clientSide.Close()
		<-done
	})

	c, err := svn.NewClient(clientSide, clientSide, "file:///fake/repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return svnfs.New(c, nil)
}

func TestFS(t *testing.T) {
	fsys := newTestFS(t)
	if err := fstest.TestFS(fsys,
		"README.md",
		"trunk/main.go",
		"trunk/sub/nested.txt",
	); err != nil {
		t.Fatal(err)
	}
}

func TestReadFile(t *testing.T) {
	fsys := newTestFS(t)
	content, err := fsys.ReadFile("trunk/main.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "package main\n" {
		t.Errorf("ReadFile = %q, want %q", content, "package main\n")
	}
}

func TestStatDirectory(t *testing.T) {
	fsys := newTestFS(t)
	info, err := fsys.Stat("trunk")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Stat(trunk).IsDir() = false, want true")
	}
	if info.Name() != "trunk" {
		t.Errorf("Stat(trunk).Name() = %q, want %q", info.Name(), "trunk")
	}
	// A real svnserve reports a directory's size as its own "invalid
	// size" sentinel (all bits set); Size must not leak that through as
	// a nonsensical negative number.
	if info.Size() != 0 {
		t.Errorf("Stat(trunk).Size() = %d, want 0", info.Size())
	}
}

func TestReadDirNamesAreBareChildNames(t *testing.T) {
	fsys := newTestFS(t)
	entries, err := fsys.ReadDir("trunk")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
		if strings.Contains(e.Name(), "/") {
			t.Errorf("entry name %q contains a slash; readDir should strip the queried-path prefix", e.Name())
		}
	}
	for _, want := range []string{"main.go", "sub"} {
		if !got[want] {
			t.Errorf("ReadDir(trunk) missing entry %q, got %v", want, got)
		}
	}
}

func TestOpenNonexistentFile(t *testing.T) {
	fsys := newTestFS(t)
	_, err := fsys.Open("does/not/exist")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open error = %v, want errors.Is(err, fs.ErrNotExist)", err)
	}
}

// writeSuccess writes a "( success params )" command-response frame,
// building its wire form from the exported Marshal/Item primitives instead
// of hand-counting string-length prefixes.
func writeSuccess(w io.Writer, params any) error {
	item, err := svn.Marshal(params)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte("( success " + item.String() + " ) "))
	return err
}

// TestOpenNotExist checks that svnfs correctly reports fs.ErrNotExist when
// svn.Client.Stat gets the real protocol's "absent entry" shape -- an
// empty success response -- which the svn.Server in this package's own
// tests can't produce (it can only fail the whole command), so this test
// drives the wire by hand instead.
func TestOpenNotExist(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	done := make(chan error, 1)
	go func() {
		z := svn.NewItemizer(serverSide)

		// Greeting, client's response, auth-request (no mechanisms, so
		// the client won't reply to it), repos-info.
		if err := writeSuccess(serverSide, []any{2, 2, []any{}, []any{}}); err != nil {
			done <- err
			return
		}
		if _, err := z.Item(); err != nil {
			done <- err
			return
		}
		if err := writeSuccess(serverSide, []any{[]any{}, []byte{}}); err != nil {
			done <- err
			return
		}
		if err := writeSuccess(serverSide, []any{
			[]byte("fake-uuid"), []byte("file:///fake/repo"), []any{},
		}); err != nil {
			done <- err
			return
		}

		// The "stat" command: consume it, answer with an empty
		// auth-request, then an empty response -- no entry.
		if _, err := z.Item(); err != nil {
			done <- err
			return
		}
		if err := writeSuccess(serverSide, []any{[]any{}, []byte{}}); err != nil {
			done <- err
			return
		}
		if err := writeSuccess(serverSide, []any{}); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	c, err := svn.NewClient(clientSide, clientSide, "file:///fake/repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	fsys := svnfs.New(c, nil)

	if _, err := fsys.Open("missing.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(missing.txt) error = %v, want errors.Is(err, fs.ErrNotExist)", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}
