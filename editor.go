package svn

import (
	"errors"
	"fmt"
	"strconv"
)

// EditorCopyFrom describes the copy-from source of an AddDir or AddFile
// call, when the new node being added is reported as a copy of something
// else already in the repository, rather than being brand new.
type EditorCopyFrom struct {
	// Path is the copy source's path, as of Rev.
	Path string
	// Rev is the revision the copy was made from.
	Rev uint
}

// editorNodeKind distinguishes a directory token from a file token on
// EditorWriter's stack of currently open nodes.
type editorNodeKind int

const (
	editorDir editorNodeKind = iota
	editorFile
)

// editorNode is one entry on EditorWriter's stack: a token it has handed
// out via an "open-root"/"add-dir"/"open-dir"/"add-file"/"open-file"
// command, not yet closed.
type editorNode struct {
	token string
	kind  editorNodeKind
}

// EditorWriter builds an Editor Command Set sequence -- the shape
// [Server.FinishReport] must return -- as a slice of [Item], so a caller
// can describe a tree of changes with typed method calls instead of
// constructing raw Items by hand. The zero value is not ready to use;
// create one with [NewEditorWriter].
//
// Every Add*/Open* call (other than [EditorWriter.OpenRoot], which starts
// the tree) adds a node as a child of whichever directory is currently
// open, and -- for AddDir/OpenDir/AddFile/OpenFile -- pushes that node
// onto an internal stack, becoming the new "currently open" node. The
// matching Close* call must be the next one made against that node,
// enforcing strict LIFO nesting: a file opened with AddFile must be
// closed with [EditorWriter.CloseFile] before its parent directory can be
// closed with [EditorWriter.CloseDir], and so on up to the root. This
// mirrors the natural, recursive way a tree of changes is described, and
// catches a caller forgetting to close a node before its sibling or
// parent -- see [EditorWriter.Items].
//
// A path argument (to AddDir, AddFile, DeleteEntry, AbsentDir or
// AbsentFile) is always the full path from the edit's root, e.g.
// "trunk/main.go", not just "main.go" relative to its immediate parent --
// confirmed against a real svnserve's own output, since neither the
// protocol spec text nor the grammar it gives says so explicitly.
//
// Tokens (the opaque strings the wire protocol uses to refer to a
// previously opened node) are entirely an implementation detail
// EditorWriter manages itself -- confirmed against a real svnserve, whose
// own tokens are nothing more than an incrementing counter (e.g. "d0",
// "c1", "d2", ...; any unique string works just as well). No EditorWriter
// method exposes or accepts one.
type EditorWriter struct {
	items     []Item
	stack     []editorNode
	nextToken int
	started   bool
}

// NewEditorWriter returns an EditorWriter ready to build an Editor Command
// Set sequence, starting with an optional [EditorWriter.TargetRev] and
// then [EditorWriter.OpenRoot].
func NewEditorWriter() *EditorWriter {
	return &EditorWriter{}
}

// emit Marshals command and its params together and appends the result.
func (e *EditorWriter) emit(command string, params []any) error {
	item, err := Marshal([]any{command, params})
	if err != nil {
		return err
	}
	e.items = append(e.items, item)
	return nil
}

func (e *EditorWriter) pushToken(kind editorNodeKind) string {
	token := strconv.Itoa(e.nextToken)
	e.nextToken++
	e.stack = append(e.stack, editorNode{token: token, kind: kind})
	return token
}

// topDir returns the token of the currently open directory, or an error
// if there isn't one (either nothing is open yet, or a file is).
func (e *EditorWriter) topDir() (string, error) {
	if len(e.stack) == 0 {
		return "", errors.New("svn: EditorWriter: no directory is open (call OpenRoot first)")
	}
	top := e.stack[len(e.stack)-1]
	if top.kind != editorDir {
		return "", fmt.Errorf("svn: EditorWriter: a file is currently open; close it first")
	}
	return top.token, nil
}

// topFile returns the token of the currently open file, or an error if
// there isn't one.
func (e *EditorWriter) topFile() (string, error) {
	if len(e.stack) == 0 {
		return "", errors.New("svn: EditorWriter: no file is open")
	}
	top := e.stack[len(e.stack)-1]
	if top.kind != editorFile {
		return "", errors.New("svn: EditorWriter: no file is open (a directory is)")
	}
	return top.token, nil
}

// optionalString returns the wire representation of a "[ x:string ]"
// optional value: an empty list if value is nil, or a list holding value
// directly (not nested another level) otherwise -- the same shape
// confirmed for ChangedPath's Copy/Info groups in types.go. A non-nil,
// empty value ([]byte{}) is present (an empty string), not absent.
func optionalString(value []byte) []any {
	if value == nil {
		return []any{}
	}
	return []any{value}
}

// TargetRev reports the revision the following editor sequence brings
// the client to. It is optional, but a real svnserve always sends it; if
// called at all, it must be the first call made, before OpenRoot.
func (e *EditorWriter) TargetRev(rev uint) error {
	if e.started {
		return errors.New("svn: EditorWriter: TargetRev must be called before OpenRoot")
	}
	return e.emit("target-rev", []any{rev})
}

// OpenRoot starts the edit, opening the root directory, and must be
// called exactly once, before anything else other than TargetRev. rev,
// if non-nil, is the root directory's own last-changed revision.
func (e *EditorWriter) OpenRoot(rev *uint) error {
	if e.started {
		return errors.New("svn: EditorWriter: OpenRoot already called")
	}
	e.started = true
	token := e.pushToken(editorDir)
	var revGroup []any
	if rev != nil {
		revGroup = []any{*rev}
	} else {
		revGroup = []any{}
	}
	return e.emit("open-root", []any{revGroup, []byte(token)})
}

func copyFromGroup(copyFrom *EditorCopyFrom) []any {
	if copyFrom == nil {
		return []any{}
	}
	return []any{[]byte(copyFrom.Path), copyFrom.Rev}
}

// AddDir adds a new directory at path, as a child of the currently open
// directory, and makes it the currently open directory in turn. If
// copyFrom is non-nil, the directory is reported as a copy of that path
// at that revision instead of being brand new.
func (e *EditorWriter) AddDir(path string, copyFrom *EditorCopyFrom) error {
	parent, err := e.topDir()
	if err != nil {
		return err
	}
	token := e.pushToken(editorDir)
	return e.emit("add-dir", []any{[]byte(path), []byte(parent), []byte(token), copyFromGroup(copyFrom)})
}

// OpenDir is like AddDir, but for a directory the client already has:
// rev is the revision of the directory being opened, as the client
// already has it. rev is sent wrapped as a "[ rev:number ]" optional
// value -- confirmed against a real svnserve to be how a real client
// expects it, contrary to a first, textually-literal reading of the
// protocol's own grammar for this command, which shows it as a bare,
// non-bracketed rev:number (a real client rejects the bare form with
// "E210004: Malformed network data"; see CLAUDE.md).
func (e *EditorWriter) OpenDir(path string, rev uint) error {
	parent, err := e.topDir()
	if err != nil {
		return err
	}
	token := e.pushToken(editorDir)
	return e.emit("open-dir", []any{[]byte(path), []byte(parent), []byte(token), []any{rev}})
}

// ChangeDirProp sets property name on the currently open directory to
// value, or deletes it if value is nil.
func (e *EditorWriter) ChangeDirProp(name string, value []byte) error {
	token, err := e.topDir()
	if err != nil {
		return err
	}
	return e.emit("change-dir-prop", []any{[]byte(token), []byte(name), optionalString(value)})
}

// CloseDir closes the currently open directory. Every node added as one
// of its children (directly or, since Close* pops exactly one level,
// transitively through further nesting) must already be closed.
func (e *EditorWriter) CloseDir() error {
	token, err := e.topDir()
	if err != nil {
		return err
	}
	e.stack = e.stack[:len(e.stack)-1]
	return e.emit("close-dir", []any{[]byte(token)})
}

// AbsentDir reports that path, a child of the currently open directory,
// exists but is deliberately not being described further (e.g. because
// access to it is denied).
func (e *EditorWriter) AbsentDir(path string) error {
	parent, err := e.topDir()
	if err != nil {
		return err
	}
	return e.emit("absent-dir", []any{[]byte(path), []byte(parent)})
}

// AddFile adds a new file at path, as a child of the currently open
// directory, and makes it the currently open file. It must be followed
// by CloseFile (optionally preceded by ApplyTextdelta and/or
// ChangeFileProp calls) before any sibling or the parent directory. If
// copyFrom is non-nil, the file is reported as a copy of that path at
// that revision instead of being brand new.
func (e *EditorWriter) AddFile(path string, copyFrom *EditorCopyFrom) error {
	parent, err := e.topDir()
	if err != nil {
		return err
	}
	token := e.pushToken(editorFile)
	return e.emit("add-file", []any{[]byte(path), []byte(parent), []byte(token), copyFromGroup(copyFrom)})
}

// OpenFile is like AddFile, but for a file the client already has: rev is
// the revision of the file being opened, as the client already has it.
// Wrapped as a "[ rev:number ]" optional value on the wire -- see
// OpenDir's doc comment for why.
func (e *EditorWriter) OpenFile(path string, rev uint) error {
	parent, err := e.topDir()
	if err != nil {
		return err
	}
	token := e.pushToken(editorFile)
	return e.emit("open-file", []any{[]byte(path), []byte(parent), []byte(token), []any{rev}})
}

// ApplyTextdelta sets the currently open file's content to content, sent
// as a single self-contained svndiff window with no source (see
// [EncodeSvndiff]) -- so it replaces whatever the client had outright,
// rather than patching it; there is no support yet for sending an
// incremental delta against a real base. baseChecksum, if non-nil, is the
// MD5 checksum (as a lowercase hex string) of the content the client is
// expected to already have; pass nil when there is none, as for a brand
// new file.
func (e *EditorWriter) ApplyTextdelta(content []byte, baseChecksum []byte) error {
	token, err := e.topFile()
	if err != nil {
		return err
	}
	if err := e.emit("apply-textdelta", []any{[]byte(token), optionalString(baseChecksum)}); err != nil {
		return err
	}
	if err := e.emit("textdelta-chunk", []any{[]byte(token), EncodeSvndiff(content)}); err != nil {
		return err
	}
	return e.emit("textdelta-end", []any{[]byte(token)})
}

// ChangeFileProp is like [EditorWriter.ChangeDirProp], but for the
// currently open file.
func (e *EditorWriter) ChangeFileProp(name string, value []byte) error {
	token, err := e.topFile()
	if err != nil {
		return err
	}
	return e.emit("change-file-prop", []any{[]byte(token), []byte(name), optionalString(value)})
}

// CloseFile closes the currently open file. textChecksum, if non-nil, is
// the MD5 checksum (as a lowercase hex string) of the file's final
// content, as a real svnserve always includes for a file it just fully
// described.
func (e *EditorWriter) CloseFile(textChecksum []byte) error {
	token, err := e.topFile()
	if err != nil {
		return err
	}
	e.stack = e.stack[:len(e.stack)-1]
	return e.emit("close-file", []any{[]byte(token), optionalString(textChecksum)})
}

// AbsentFile is like [EditorWriter.AbsentDir], but for a file.
func (e *EditorWriter) AbsentFile(path string) error {
	parent, err := e.topDir()
	if err != nil {
		return err
	}
	return e.emit("absent-file", []any{[]byte(path), []byte(parent)})
}

// DeleteEntry reports that path, a child of the currently open directory,
// no longer exists (or is about to be replaced by whatever AddDir/AddFile
// call follows). rev, if non-nil, is the revision at which path was
// determined to be gone (a real svnserve sends the update's own target
// revision here) -- confirmed against a real svnserve to be optional,
// contrary to a first, textually-literal reading of the protocol's own
// grammar for this command, which shows it as a bare, non-bracketed
// rev:number (see CLAUDE.md).
func (e *EditorWriter) DeleteEntry(path string, rev *uint) error {
	parent, err := e.topDir()
	if err != nil {
		return err
	}
	return e.emit("delete-entry", []any{[]byte(path), optionalUint(rev), []byte(parent)})
}

// optionalUint returns the wire representation of a "[ x:number ]"
// optional value: an empty list if rev is nil, or a list holding *rev
// directly otherwise.
func optionalUint(rev *uint) []any {
	if rev == nil {
		return []any{}
	}
	return []any{*rev}
}

// Items returns the accumulated Editor Command Set sequence, ready to
// return from [Server.FinishReport]. It returns an error if OpenRoot was
// never called, or if any node opened along the way (including the root
// itself) was never closed.
func (e *EditorWriter) Items() ([]Item, error) {
	if !e.started {
		return nil, errors.New("svn: EditorWriter: OpenRoot was never called")
	}
	if len(e.stack) != 0 {
		return nil, fmt.Errorf("svn: EditorWriter: %d node(s) still open (missing Close*)", len(e.stack))
	}
	return e.items, nil
}
