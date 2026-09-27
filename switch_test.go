package svn

import (
	"io/fs"
	"strings"
	"testing"
)

// switchFakeNode is one entry in switchFakeTree.
type switchFakeNode struct {
	kind    string // "file" or "dir"
	content string
	rev     uint
}

// switchFakeTree is a small in-memory repository for SwitchEdit tests,
// with two independent trees ("trunk" and "branch") corresponding to
// each other only by name, not by any shared history:
//
//	trunk/a.txt          (modified on branch)
//	trunk/onlytrunk.txt  (missing on branch: deleted by the switch)
//	trunk/samerev.txt    (different content, but coincidentally the same
//	                      CreatedRev as branch/samerev.txt -- see below)
//	branch/a.txt         (trunk's counterpart, different content/rev)
//	branch/onlybranch.txt (missing on trunk: added by the switch)
//	branch/samerev.txt   (trunk's counterpart, same CreatedRev, different content)
func switchFakeTree() map[string]switchFakeNode {
	return map[string]switchFakeNode{
		"":                      {kind: "dir", rev: 1},
		"trunk":                 {kind: "dir", rev: 1},
		"trunk/a.txt":           {kind: "file", rev: 1, content: "hello\n"},
		"trunk/onlytrunk.txt":   {kind: "file", rev: 1, content: "only in trunk\n"},
		"trunk/samerev.txt":     {kind: "file", rev: 3, content: "trunk version\n"},
		"branch":                {kind: "dir", rev: 1},
		"branch/a.txt":          {kind: "file", rev: 2, content: "hello from branch\n"},
		"branch/onlybranch.txt": {kind: "file", rev: 1, content: "only in branch\n"},
		"branch/samerev.txt":    {kind: "file", rev: 3, content: "branch version\n"},
	}
}

// newSwitchFakeServer returns a Server whose List/GetFile are backed by
// switchFakeTree, with every path already relative to the repository
// root (no session-anchor resolution, as in checkout_test.go's
// newCheckoutFakeServer).
func newSwitchFakeServer() *Server {
	tree := switchFakeTree()
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
		out := []Dirent{{Path: "/" + path, Kind: "dir", CreatedRev: n.rev}}
		for p, e := range tree {
			if p == path || !strings.HasPrefix(p, prefix) {
				continue
			}
			rest := strings.TrimPrefix(p, prefix)
			if rest == "" || strings.Contains(rest, "/") {
				continue
			}
			out = append(out, Dirent{Path: "/" + p, Kind: e.kind, CreatedRev: e.rev})
		}
		return out, nil
	}
	s.GetFile = func(path string, rev *uint, wantProps, wantContents bool) (uint, []PropList, []byte, error) {
		n, ok := tree[path]
		if !ok || n.kind != "file" {
			return 0, nil, nil, fs.ErrNotExist
		}
		return n.rev, nil, []byte(n.content), nil
	}
	s.ReposInfo.UUID = "9d3c8b6e-0000-0000-0000-000000000000"
	return &s
}

// TestSwitchEditTreeShape checks that SwitchEdit, diffing "trunk" against
// "branch" in switchFakeTree, describes exactly what differs between the
// two locations: a.txt is open (modified, since branch's own CreatedRev
// differs from trunk's), onlytrunk.txt is deleted (present only under
// trunk), and onlybranch.txt is added (present only under branch) --
// exercising add/delete/modify all in the same switch, purely by name
// correspondence rather than any shared revision history.
func TestSwitchEditTreeShape(t *testing.T) {
	s := newSwitchFakeServer()
	items, err := s.SwitchEdit("trunk", "branch", "", 1, 2)
	if err != nil {
		t.Fatalf("SwitchEdit: %v", err)
	}

	openFiles := itemsNamed(items, "open-file")
	aTxt, ok := openFiles["a.txt"]
	if !ok {
		t.Fatalf("a.txt was not open-file'd (items:\n%s)", itemLines(items))
	}
	if got := aTxt.List[1].List[3].List[0].Number; got != 1 {
		t.Errorf("open-file a.txt rev = %d, want 1 (the client's own baseline)", got)
	}

	if _, ok := itemsNamed(items, "delete-entry")["onlytrunk.txt"]; !ok {
		t.Errorf("onlytrunk.txt was not delete-entry'd (items:\n%s)", itemLines(items))
	}

	addFiles := itemsNamed(items, "add-file")
	onlyBranch, ok := addFiles["onlybranch.txt"]
	if !ok {
		t.Fatalf("onlybranch.txt was not add-file'd (items:\n%s)", itemLines(items))
	}

	checkContent := func(item Item, want string) {
		t.Helper()
		idx := -1
		for i := range items {
			if items[i].String() == item.String() {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("could not locate %s in the item sequence", item)
		}
		token := item.List[1].List[2].Text
		var chunk *Item
		for i := idx + 1; i < len(items); i++ {
			it := items[i]
			if it.Type == ListType && len(it.List) == 2 && it.List[0].Text == "textdelta-chunk" && it.List[1].List[0].Text == token {
				chunk = &it
				break
			}
		}
		if chunk == nil {
			t.Fatalf("no textdelta-chunk found for %s (token %q)", item, token)
		}
		got, err := decodeSvndiff(nil, []byte(chunk.List[1].List[1].Text))
		if err != nil {
			t.Fatalf("decodeSvndiff: %v", err)
		}
		if string(got) != want {
			t.Errorf("content = %q, want %q", got, want)
		}
	}
	checkContent(aTxt, "hello from branch\n")
	checkContent(onlyBranch, "only in branch\n")
}

// TestSwitchEditDoesNotSkipSameCreatedRev checks that SwitchEdit never
// treats a file as unchanged just because it shares a CreatedRev with
// its counterpart on the other side: unlike UpdateEdit (where a matching
// CreatedRev is conclusive proof of no change, since it's the very same
// path at two revisions), two files at different paths can share a
// CreatedRev by pure coincidence -- e.g. both were added in the same
// commit -- despite having unrelated content, as switchFakeTree's own
// "samerev.txt" pair (present under both trunk and branch, same
// CreatedRev, different content) is built to exercise. Reported as a
// real bug: an earlier version of updateChildren/updateNavigateToTarget
// used to skip such a file, sending its old (trunk) content to a client
// switching to branch instead of the new one.
func TestSwitchEditDoesNotSkipSameCreatedRev(t *testing.T) {
	s := newSwitchFakeServer()
	items, err := s.SwitchEdit("trunk", "branch", "", 1, 2)
	if err != nil {
		t.Fatalf("SwitchEdit: %v", err)
	}

	openFiles := itemsNamed(items, "open-file")
	sameRev, ok := openFiles["samerev.txt"]
	if !ok {
		t.Fatalf("samerev.txt was skipped as unchanged despite having different content on each side (items:\n%s)", itemLines(items))
	}

	token := sameRev.List[1].List[2].Text
	var chunk *Item
	for _, it := range items {
		if it.Type == ListType && len(it.List) == 2 && it.List[0].Text == "textdelta-chunk" && it.List[1].List[0].Text == token {
			c := it
			chunk = &c
			break
		}
	}
	if chunk == nil {
		t.Fatalf("no textdelta-chunk found for samerev.txt (token %q)", token)
	}
	got, err := decodeSvndiff(nil, []byte(chunk.List[1].List[1].Text))
	if err != nil {
		t.Fatalf("decodeSvndiff: %v", err)
	}
	if want := "branch version\n"; string(got) != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}
