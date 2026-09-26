package svn

import (
	"crypto/md5"
	"fmt"
	"strings"
	"testing"
)

// checksum returns the lowercase-hex MD5 checksum EditorWriter expects for
// ApplyTextdelta/CloseFile.
func checksum(content []byte) []byte {
	return []byte(fmt.Sprintf("%x", md5.Sum(content)))
}

// TestEditorWriterTreeShape builds a small tree -- a root directory with
// one file and one subdirectory (itself with a property and a file, plus
// its own nested subdirectory with a file) -- and checks the resulting
// Editor Command Set sequence against golden text for every command,
// confirming both the exact wire shape (path is the full path from the
// root, not just the child's own name; optional groups are direct 0- or
// N-element lists, not double-nested; property/checksum values are
// length-prefixed strings, not words) and the token/nesting bookkeeping
// (sequential token allocation, correct parent references, and every
// open node closed in the right order) -- all confirmed against a real
// svnserve's own "checkout" editor sequence (see CLAUDE.md).
func TestEditorWriterTreeShape(t *testing.T) {
	e := NewEditorWriter()
	rootRev := uint(1)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	must(e.TargetRev(2))
	must(e.OpenRoot(&rootRev))

	must(e.AddFile("README.md", nil))
	readme := []byte("hello world\n")
	must(e.ApplyTextdelta(readme, nil))
	must(e.CloseFile(checksum(readme)))

	must(e.AddDir("trunk", nil))
	must(e.ChangeDirProp("svn:ignore", []byte("*.tmp\n")))

	must(e.AddFile("trunk/main.go", nil))
	mainGo := []byte("package main\n")
	must(e.ApplyTextdelta(mainGo, nil))
	must(e.CloseFile(checksum(mainGo)))

	must(e.AddDir("trunk/sub", nil))
	must(e.AddFile("trunk/sub/nested.txt", nil))
	nested := []byte("nested\n")
	must(e.ApplyTextdelta(nested, nil))
	must(e.CloseFile(checksum(nested)))
	must(e.CloseDir()) // trunk/sub

	must(e.CloseDir()) // trunk
	must(e.CloseDir()) // root

	items, err := e.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}

	var got []string
	for _, it := range items {
		got = append(got, it.String())
	}

	want := []string{
		`( target-rev ( 2 ) )`,
		`( open-root ( ( 1 ) 1:0 ) )`,
		`( add-file ( 9:README.md 1:0 1:1 ( ) ) )`,
		`( apply-textdelta ( 1:1 ( ) ) )`,
		// textdelta-chunk's payload is EncodeSvndiff's binary output;
		// checked separately below rather than pinned as golden text.
		"", // placeholder, replaced below
		`( textdelta-end ( 1:1 ) )`,
		fmt.Sprintf(`( close-file ( 1:1 ( 32:%x ) ) )`, md5.Sum(readme)),
		`( add-dir ( 5:trunk 1:0 1:2 ( ) ) )`,
		`( change-dir-prop ( 1:2 10:svn:ignore ( 6:*.tmp` + "\n" + ` ) ) )`,
		`( add-file ( 13:trunk/main.go 1:2 1:3 ( ) ) )`,
		`( apply-textdelta ( 1:3 ( ) ) )`,
		"",
		`( textdelta-end ( 1:3 ) )`,
		fmt.Sprintf(`( close-file ( 1:3 ( 32:%x ) ) )`, md5.Sum(mainGo)),
		`( add-dir ( 9:trunk/sub 1:2 1:4 ( ) ) )`,
		`( add-file ( 20:trunk/sub/nested.txt 1:4 1:5 ( ) ) )`,
		`( apply-textdelta ( 1:5 ( ) ) )`,
		"",
		`( textdelta-end ( 1:5 ) )`,
		fmt.Sprintf(`( close-file ( 1:5 ( 32:%x ) ) )`, md5.Sum(nested)),
		`( close-dir ( 1:4 ) )`,
		`( close-dir ( 1:2 ) )`,
		`( close-dir ( 1:0 ) )`,
	}

	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d\ngot:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if want[i] == "" {
			continue // textdelta-chunk: checked below
		}
		if got[i] != want[i] {
			t.Errorf("item %d = %s, want %s", i, got[i], want[i])
		}
	}

	// Verify every textdelta-chunk item separately: its token must match
	// the surrounding file's, and its payload must decode back (with no
	// source, since ApplyTextdelta never sends one) to the exact content
	// given.
	chunks := map[string][]byte{
		"1": readme, "3": mainGo, "5": nested,
	}
	for i, it := range items {
		if it.Type != ListType || len(it.List) != 2 || it.List[0].Text != "textdelta-chunk" {
			continue
		}
		params := it.List[1].List
		if len(params) != 2 {
			t.Fatalf("item %d: textdelta-chunk has %d params, want 2", i, len(params))
		}
		token := params[0].Text
		want, ok := chunks[token]
		if !ok {
			t.Fatalf("item %d: unexpected textdelta-chunk token %q", i, token)
		}
		got, err := decodeSvndiff(nil, []byte(params[1].Text))
		if err != nil {
			t.Fatalf("item %d: decodeSvndiff: %v", i, err)
		}
		if string(got) != string(want) {
			t.Errorf("item %d: textdelta-chunk content = %q, want %q", i, got, want)
		}
	}
}

// TestEditorWriterNestingErrors checks that EditorWriter rejects the
// caller mistakes its stack-based nesting is meant to catch: acting on a
// node before it's open, closing a directory while a child file is still
// open, and finishing before every open node is closed.
func TestEditorWriterNestingErrors(t *testing.T) {
	t.Run("AddDir before OpenRoot", func(t *testing.T) {
		e := NewEditorWriter()
		if err := e.AddDir("trunk", nil); err == nil {
			t.Error("want an error, got nil")
		}
	})

	t.Run("OpenRoot called twice", func(t *testing.T) {
		e := NewEditorWriter()
		rev := uint(1)
		if err := e.OpenRoot(&rev); err != nil {
			t.Fatalf("first OpenRoot: %v", err)
		}
		if err := e.OpenRoot(&rev); err == nil {
			t.Error("want an error on the second OpenRoot, got nil")
		}
	})

	t.Run("TargetRev after OpenRoot", func(t *testing.T) {
		e := NewEditorWriter()
		rev := uint(1)
		if err := e.OpenRoot(&rev); err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		if err := e.TargetRev(2); err == nil {
			t.Error("want an error, got nil")
		}
	})

	t.Run("CloseDir while a file is open", func(t *testing.T) {
		e := NewEditorWriter()
		rev := uint(1)
		if err := e.OpenRoot(&rev); err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		if err := e.AddFile("foo.txt", nil); err != nil {
			t.Fatalf("AddFile: %v", err)
		}
		if err := e.CloseDir(); err == nil {
			t.Error("want an error, got nil")
		}
	})

	t.Run("ChangeFileProp with only a directory open", func(t *testing.T) {
		e := NewEditorWriter()
		rev := uint(1)
		if err := e.OpenRoot(&rev); err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		if err := e.ChangeFileProp("svn:mime-type", []byte("text/plain")); err == nil {
			t.Error("want an error, got nil")
		}
	})

	t.Run("Items with an unclosed node", func(t *testing.T) {
		e := NewEditorWriter()
		rev := uint(1)
		if err := e.OpenRoot(&rev); err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		if _, err := e.Items(); err == nil {
			t.Error("want an error, got nil")
		}
	})

	t.Run("Items before OpenRoot", func(t *testing.T) {
		e := NewEditorWriter()
		if _, err := e.Items(); err == nil {
			t.Error("want an error, got nil")
		}
	})
}

// TestEditorWriterChangeDirPropNilVsEmpty checks that a nil value deletes
// a property (an absent "[ value:string ]" group) while a non-nil, empty
// value sets it to the empty string (a present group holding an empty
// string) -- these are different wire shapes, both meaningful.
func TestEditorWriterChangeDirPropNilVsEmpty(t *testing.T) {
	e := NewEditorWriter()
	rev := uint(1)
	if err := e.OpenRoot(&rev); err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	if err := e.ChangeDirProp("svn:ignore", nil); err != nil {
		t.Fatalf("ChangeDirProp(nil): %v", err)
	}
	if err := e.ChangeDirProp("svn:ignore", []byte{}); err != nil {
		t.Fatalf("ChangeDirProp(empty): %v", err)
	}
	if err := e.CloseDir(); err != nil {
		t.Fatalf("CloseDir: %v", err)
	}
	items, err := e.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	want := []string{
		`( open-root ( ( 1 ) 1:0 ) )`,
		`( change-dir-prop ( 1:0 10:svn:ignore ( ) ) )`,
		`( change-dir-prop ( 1:0 10:svn:ignore ( 0: ) ) )`,
		`( close-dir ( 1:0 ) )`,
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d", len(items), len(want))
	}
	for i := range want {
		if items[i].String() != want[i] {
			t.Errorf("item %d = %s, want %s", i, items[i].String(), want[i])
		}
	}
}
