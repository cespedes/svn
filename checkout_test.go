package svn

import (
	"crypto/md5"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// checkoutFakeNode is one entry in checkoutFakeTree.
type checkoutFakeNode struct {
	kind    string // "file" or "dir"
	content string
}

// checkoutFakeTree is a small in-memory repository for CheckoutEdit tests:
//
//	README.md
//	trunk/main.go
//	trunk/sub/nested.txt
func checkoutFakeTree() map[string]checkoutFakeNode {
	return map[string]checkoutFakeNode{
		"":                     {kind: "dir"},
		"README.md":            {kind: "file", content: "hello world\n"},
		"trunk":                {kind: "dir"},
		"trunk/main.go":        {kind: "file", content: "package main\n"},
		"trunk/sub":            {kind: "dir"},
		"trunk/sub/nested.txt": {kind: "file", content: "nested\n"},
	}
}

// newCheckoutFakeServer returns a Server whose List/GetFile are backed by
// checkoutFakeTree, with every path already relative to whatever root
// CheckoutEdit is asked to walk (i.e. no session-anchor resolution, since
// none of these tests exercise one -- a real List/GetFile would
// otherwise need one, to resolve a session anchored below the
// repository root).
func newCheckoutFakeServer() *Server {
	tree := checkoutFakeTree()
	var s Server
	s.List = func(path string, rev *uint, depth string, fields, pattern []string) ([]Dirent, error) {
		n, ok := tree[path]
		if !ok || n.kind != "dir" {
			return nil, fs.ErrNotExist
		}
		prefix := path
		if prefix != "" {
			prefix += "/"
		}
		out := []Dirent{{Path: "/" + path, Kind: "dir"}}
		for p, e := range tree {
			if p == path || !strings.HasPrefix(p, prefix) {
				continue
			}
			rest := strings.TrimPrefix(p, prefix)
			if rest == "" || strings.Contains(rest, "/") {
				continue // not a direct child
			}
			out = append(out, Dirent{
				Path: "/" + p, Kind: e.kind, Size: uint64(len(e.content)),
				CreatedRev: 3, CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
			})
		}
		return out, nil
	}
	s.GetFile = func(path string, rev *uint, wantProps, wantContents bool) (uint, []PropList, []byte, error) {
		n, ok := tree[path]
		if !ok || n.kind != "file" {
			return 0, nil, nil, fs.ErrNotExist
		}
		return 1, nil, []byte(n.content), nil
	}
	s.ReposInfo.UUID = "9d3c8b6e-0000-0000-0000-000000000000"
	return &s
}

// TestCheckoutEditTreeShape checks that CheckoutEdit, walking a small
// nested tree from the repository root, produces a correctly nested
// Editor Command Set sequence with every path relative to the edit's own
// root (e.g. "trunk/main.go", not "main.go") -- the exact case a plain
// "svn checkout" of the whole repository needs. Since checkoutFakeTree's
// entries come back from a Go map (randomized order), this checks
// structure -- every expected file/dir is present, correctly nested, with
// the right content -- rather than one pinned golden sequence.
func TestCheckoutEditTreeShape(t *testing.T) {
	s := newCheckoutFakeServer()

	items, err := s.CheckoutEdit("", 2)
	if err != nil {
		t.Fatalf("CheckoutEdit: %v", err)
	}

	if items[0].String() != `( target-rev ( 2 ) )` {
		t.Errorf("items[0] = %s, want target-rev", items[0])
	}
	if items[1].String() != `( open-root ( ( 0 ) 1:0 ) )` {
		t.Errorf("items[1] = %s, want open-root", items[1])
	}
	if last := items[len(items)-1]; last.String() != `( close-dir ( 1:0 ) )` {
		t.Errorf("last item = %s, want close-dir of the root token", last)
	}

	checkFile := func(wirePath string, content []byte) {
		t.Helper()
		for i, it := range items {
			if it.Type != ListType || len(it.List) != 2 || it.List[0].Text != "add-file" {
				continue
			}
			params := it.List[1].List
			if params[0].Text != wirePath {
				continue
			}
			token := params[2].Text

			// The 4 svn:entry:* pseudo-properties a real client's local
			// metadata database needs (confirmed: a missing committed-rev
			// crashes a real "svn checkout" outright) come right after
			// add-file, before any content.
			propNames := []string{
				"svn:entry:committed-rev", "svn:entry:committed-date",
				"svn:entry:last-author", "svn:entry:uuid",
			}
			for j, name := range propNames {
				p := items[i+1+j]
				if p.List[0].Text != "change-file-prop" || p.List[1].List[0].Text != token || p.List[1].List[1].Text != name {
					t.Errorf("item %d for %s = %s, want change-file-prop %s", i+1+j, wirePath, p, name)
				}
			}
			if got := items[i+1].List[1].List[2].List[0].Text; got != "3" {
				t.Errorf("%s committed-rev = %q, want %q", wirePath, got, "3")
			}
			if got := items[i+4].List[1].List[2].List[0].Text; got != "9d3c8b6e-0000-0000-0000-000000000000" {
				t.Errorf("%s uuid = %q, want the repository's own UUID", wirePath, got)
			}

			delta, chunk, end, closeItem := items[i+5], items[i+6], items[i+7], items[i+8]
			if delta.List[0].Text != "apply-textdelta" || delta.List[1].List[0].Text != token {
				t.Errorf("add-file %s not immediately followed by its own apply-textdelta", wirePath)
			}
			if chunk.List[0].Text != "textdelta-chunk" || chunk.List[1].List[0].Text != token {
				t.Errorf("expected textdelta-chunk for %s right after apply-textdelta", wirePath)
			}
			decoded, err := decodeSvndiff(nil, []byte(chunk.List[1].List[1].Text))
			if err != nil {
				t.Errorf("decodeSvndiff for %s: %v", wirePath, err)
			} else if string(decoded) != string(content) {
				t.Errorf("content for %s = %q, want %q", wirePath, decoded, content)
			}
			if end.List[0].Text != "textdelta-end" || end.List[1].List[0].Text != token {
				t.Errorf("expected textdelta-end for %s right after its chunk", wirePath)
			}
			wantChecksum := fmt.Sprintf("%x", md5.Sum(content))
			if closeItem.List[0].Text != "close-file" || closeItem.List[1].List[0].Text != token ||
				closeItem.List[1].List[1].List[0].Text != wantChecksum {
				t.Errorf("close-file for %s = %s, want token %s checksum %s", wirePath, closeItem, token, wantChecksum)
			}
			return
		}
		t.Errorf("add-file for %q not found in:\n%s", wirePath, itemLines(items))
	}
	checkFile("README.md", []byte("hello world\n"))
	checkFile("trunk/main.go", []byte("package main\n"))
	checkFile("trunk/sub/nested.txt", []byte("nested\n"))

	// Confirm real nesting (not just a flat sequence that happens to
	// balance): "trunk/sub"'s parent token must be "trunk"'s own token,
	// and "trunk/sub/nested.txt"'s parent must be "trunk/sub"'s.
	tokenOf := map[string]string{}
	parentOf := map[string]string{}
	for _, it := range items {
		if it.Type != ListType || len(it.List) != 2 {
			continue
		}
		if it.List[0].Text == "add-dir" || it.List[0].Text == "add-file" {
			p := it.List[1].List
			tokenOf[p[0].Text] = p[2].Text
			parentOf[p[0].Text] = p[1].Text
		}
	}
	if parentOf["trunk/sub"] != tokenOf["trunk"] {
		t.Errorf("trunk/sub's parent token = %s, want trunk's own token %s", parentOf["trunk/sub"], tokenOf["trunk"])
	}
	if parentOf["trunk/sub/nested.txt"] != tokenOf["trunk/sub"] {
		t.Errorf("trunk/sub/nested.txt's parent token = %s, want trunk/sub's own token %s", parentOf["trunk/sub/nested.txt"], tokenOf["trunk/sub"])
	}
	if parentOf["README.md"] != "0" {
		// The root's own token is always "0" (the first one allocated).
		t.Errorf("README.md's parent token = %s, want the root's own token 0", parentOf["README.md"])
	}
}

func itemLines(items []Item) string {
	s := ""
	for _, it := range items {
		s += it.String() + "\n"
	}
	return s
}

func TestIsPlainCheckout(t *testing.T) {
	tests := []struct {
		name   string
		report []ReportedPath
		want   bool
	}{
		{"plain checkout", []ReportedPath{{Path: "", Rev: 5, StartEmpty: true}}, true},
		{"not start-empty (a real update)", []ReportedPath{{Path: "", Rev: 5, StartEmpty: false}}, false},
		{"non-root path", []ReportedPath{{Path: "trunk", Rev: 5, StartEmpty: true}}, false},
		{"more than one entry", []ReportedPath{
			{Path: "", Rev: 5, StartEmpty: true},
			{Path: "trunk", Rev: 5, StartEmpty: false},
		}, false},
		{"empty report", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPlainCheckout(tt.report); got != tt.want {
				t.Errorf("IsPlainCheckout(%+v) = %v, want %v", tt.report, got, tt.want)
			}
		})
	}
}

// TestSplitCheckoutEntriesAnchoredSession checks that splitCheckoutEntries
// correctly identifies the self-entry -- and childName correctly strips
// it -- even when Dirent.Path values are shaped as a real svnserve sends
// them for a session anchored below the repository root (e.g. a checkout
// of ".../repo/trunk"): repository-root-relative ("/trunk", not "/"),
// confirmed against a real svnserve to always be this way regardless of
// what path CheckoutEdit/UpdateEdit queried with (always "" for the
// report's own root). Before this, an earlier version of
// splitCheckoutEntries assumed the self-entry's Path was always
// "/" + the queried path, which only holds when the session isn't
// anchored -- confirmed to cause a real "svn checkout"/"svn update" of a
// repository subdirectory to fail outright with "E210004: Malformed
// network data".
func TestSplitCheckoutEntriesAnchoredSession(t *testing.T) {
	entries := []Dirent{
		{Path: "/trunk/sub", Kind: "dir"},
		{Path: "/trunk", Kind: "dir"},
		{Path: "/trunk/main.go", Kind: "file"},
	}
	self, children := splitCheckoutEntries(entries)
	if self == nil || self.Path != "/trunk" {
		t.Fatalf("self = %+v, want a Path of /trunk", self)
	}
	if len(children) != 2 {
		t.Fatalf("len(children) = %d, want 2 (children: %+v)", len(children), children)
	}
	for _, c := range children {
		if c.Path == self.Path {
			t.Errorf("self-entry %q leaked into children", c.Path)
		}
	}
	names := map[string]bool{}
	for _, c := range children {
		names[childName(c, self.Path)] = true
	}
	if !names["main.go"] || !names["sub"] {
		t.Errorf("childName results = %v, want exactly {main.go, sub}", names)
	}
}
