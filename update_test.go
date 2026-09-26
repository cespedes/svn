package svn

import (
	"io/fs"
	"strings"
	"testing"
)

// updateFakeNode is one entry in updateFakeTreeAt.
type updateFakeNode struct {
	kind    string // "file" or "dir"
	content string
	rev     uint
}

// updateFakeTreeAt returns a small in-memory repository's state as of
// rev (1 or 2; anything below 2 is treated as rev 1), exercising every
// UpdateEdit case between the two: README.md is unchanged, trunk/main.go
// is modified, trunk/sub is removed, and trunk/newdir/inside.txt plus
// trunk/newfile.go are added.
func updateFakeTreeAt(rev uint) map[string]updateFakeNode {
	tree := map[string]updateFakeNode{
		"":              {kind: "dir", rev: 1},
		"README.md":     {kind: "file", rev: 1, content: "hello world\n"},
		"trunk":         {kind: "dir", rev: 1},
		"trunk/main.go": {kind: "file", rev: 1, content: "package main\n"},
	}
	if rev < 2 {
		tree["trunk/sub"] = updateFakeNode{kind: "dir", rev: 1}
		tree["trunk/sub/nested.txt"] = updateFakeNode{kind: "file", rev: 1, content: "nested\n"}
		return tree
	}
	tree["trunk"] = updateFakeNode{kind: "dir", rev: 2}
	tree["trunk/main.go"] = updateFakeNode{kind: "file", rev: 2, content: "package main\n\nfunc main() {}\n"}
	tree["trunk/newdir"] = updateFakeNode{kind: "dir", rev: 2}
	tree["trunk/newdir/inside.txt"] = updateFakeNode{kind: "file", rev: 2, content: "inside\n"}
	tree["trunk/newfile.go"] = updateFakeNode{kind: "file", rev: 2, content: "package main\n\nfunc New() {}\n"}
	return tree
}

// newUpdateFakeServer returns a Server whose List/GetFile are backed by
// updateFakeTreeAt, honoring whatever rev each call is given (nil
// meaning rev 2, the "latest"), with no session-anchor resolution (see
// checkout_test.go's newCheckoutFakeServer for why that's fine here).
func newUpdateFakeServer() *Server {
	treeAt := func(rev *uint) map[string]updateFakeNode {
		if rev == nil {
			return updateFakeTreeAt(2)
		}
		return updateFakeTreeAt(*rev)
	}
	var s Server
	s.List = func(path string, rev *uint, depth string, fields, pattern []string) ([]Dirent, error) {
		tree := treeAt(rev)
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
		tree := treeAt(rev)
		n, ok := tree[path]
		if !ok || n.kind != "file" {
			return 0, nil, nil, fs.ErrNotExist
		}
		return n.rev, nil, []byte(n.content), nil
	}
	s.ReposInfo.UUID = "9d3c8b6e-0000-0000-0000-000000000000"
	return &s
}

// itemsNamed returns, among items, every command-shaped Item matching
// command, keyed by its first parameter (a StringType path/token), for
// items whose params start with a string field -- add-dir, add-file,
// open-dir, open-file and delete-entry all qualify.
func itemsNamed(items []Item, command string) map[string]Item {
	out := map[string]Item{}
	for _, it := range items {
		if it.Type != ListType || len(it.List) != 2 || it.List[0].Text != command {
			continue
		}
		out[it.List[1].List[0].Text] = it
	}
	return out
}

// TestUpdateEditTreeShape checks that UpdateEdit, diffing updateFakeTreeAt
// between rev 1 and rev 2, describes exactly what changed: an unmodified
// file is never opened at all (confirmed against a real svnserve to
// behave the same way), a modified file is opened (not added) with its
// new content, a removed entry is delete-entry'd, and new entries are
// added.
func TestUpdateEditTreeShape(t *testing.T) {
	s := newUpdateFakeServer()
	items, err := s.UpdateEdit("", "", 1, 2)
	if err != nil {
		t.Fatalf("UpdateEdit: %v", err)
	}

	for _, cmd := range []string{"add-file", "open-file"} {
		if _, ok := itemsNamed(items, cmd)["README.md"]; ok {
			t.Errorf("README.md was described via %q, want it skipped entirely (unchanged)", cmd)
		}
	}

	openFiles := itemsNamed(items, "open-file")
	mainGo, ok := openFiles["trunk/main.go"]
	if !ok {
		t.Fatalf("trunk/main.go was not open-file'd (items:\n%s)", itemLines(items))
	}
	if got := mainGo.List[1].List[3].List[0].Number; got != 1 {
		t.Errorf("open-file trunk/main.go rev = %d, want 1 (the client's own baseline)", got)
	}

	if _, ok := itemsNamed(items, "delete-entry")["trunk/sub"]; !ok {
		t.Errorf("trunk/sub was not delete-entry'd")
	}

	addFiles := itemsNamed(items, "add-file")
	inside, ok := addFiles["trunk/newdir/inside.txt"]
	if !ok {
		t.Fatalf("trunk/newdir/inside.txt was not add-file'd")
	}
	newfile, ok := addFiles["trunk/newfile.go"]
	if !ok {
		t.Fatalf("trunk/newfile.go was not add-file'd")
	}
	if _, ok := itemsNamed(items, "add-dir")["trunk/newdir"]; !ok {
		t.Errorf("trunk/newdir was not add-dir'd")
	}

	// The content that got (re)sent must decode back correctly: find the
	// item's own token, then the next textdelta-chunk for that same
	// token (an unknown, entry-props-dependent number of change-file-
	// prop items comes between the two -- see checkoutEmitEntryProps).
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
	checkContent(mainGo, "package main\n\nfunc main() {}\n")
	checkContent(inside, "inside\n")
	checkContent(newfile, "package main\n\nfunc New() {}\n")
}

func TestIsSingleRevisionUpdate(t *testing.T) {
	tests := []struct {
		name    string
		report  []ReportedPath
		wantRev uint
		wantOk  bool
	}{
		{"single-revision update", []ReportedPath{{Path: "", Rev: 5, StartEmpty: false}}, 5, true},
		{"start-empty (a checkout, not an update)", []ReportedPath{{Path: "", Rev: 5, StartEmpty: true}}, 0, false},
		{"non-root path", []ReportedPath{{Path: "trunk", Rev: 5, StartEmpty: false}}, 0, false},
		{"more than one entry (mixed revision)", []ReportedPath{
			{Path: "", Rev: 5, StartEmpty: false},
			{Path: "trunk", Rev: 3, StartEmpty: false},
		}, 0, false},
		{"empty report", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rev, ok := IsSingleRevisionUpdate(tt.report)
			if ok != tt.wantOk || (ok && rev != tt.wantRev) {
				t.Errorf("IsSingleRevisionUpdate(%+v) = (%d, %v), want (%d, %v)", tt.report, rev, ok, tt.wantRev, tt.wantOk)
			}
		})
	}
}

// TestUpdateEditMultiSegmentTarget checks that UpdateEdit, given a
// multi-segment target (e.g. "trunk/main.go", the shape a Server.Diff
// callback needs to compute via RepoRelativePath when it reuses a
// session anchored above the target's own parent -- see UpdateEdit's own
// doc comment), navigates down to just that one file without describing
// any directory strictly between path and it: no open-dir/add-dir, no
// entry-props, for "trunk" itself. Confirmed the hard way: describing an
// intermediate directory produces a bogus, doubled local path (e.g.
// ".../trunk/trunk/main.go") that a real client rejects outright once it
// tries to apply the edit.
func TestUpdateEditMultiSegmentTarget(t *testing.T) {
	s := newUpdateFakeServer()
	items, err := s.UpdateEdit("", "trunk/main.go", 1, 2)
	if err != nil {
		t.Fatalf("UpdateEdit: %v", err)
	}

	for _, it := range items {
		if it.Type == ListType && len(it.List) == 2 {
			if it.List[0].Text == "open-dir" || it.List[0].Text == "add-dir" {
				t.Errorf("unexpected %s for an intermediate directory: %s", it.List[0].Text, it)
			}
		}
	}

	openFiles := itemsNamed(items, "open-file")
	mainGo, ok := openFiles["main.go"]
	if !ok {
		t.Fatalf("main.go was not open-file'd directly as a child of the root (items:\n%s)", itemLines(items))
	}
	if got := mainGo.List[1].List[3].List[0].Number; got != 1 {
		t.Errorf("open-file main.go rev = %d, want 1 (the client's own baseline)", got)
	}

	token := mainGo.List[1].List[2].Text
	var chunk *Item
	for _, it := range items {
		if it.Type == ListType && len(it.List) == 2 && it.List[0].Text == "textdelta-chunk" && it.List[1].List[0].Text == token {
			c := it
			chunk = &c
			break
		}
	}
	if chunk == nil {
		t.Fatalf("no textdelta-chunk found for main.go (token %q)", token)
	}
	got, err := decodeSvndiff(nil, []byte(chunk.List[1].List[1].Text))
	if err != nil {
		t.Fatalf("decodeSvndiff: %v", err)
	}
	if want := "package main\n\nfunc main() {}\n"; string(got) != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}
