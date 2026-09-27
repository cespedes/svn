package svn

import (
	"crypto/md5"
	"errors"
	"fmt"
)

// Editor receives one call per command as [Client.Checkout]/[Client.Update]
// parse an incoming Editor Command Set sequence off the wire -- the
// reverse of what [EditorWriter] does for the server side. Each call
// happens in the exact order the server sent the underlying commands,
// following the same LIFO nesting EditorWriter enforces when writing one:
// an AddDir/OpenDir's own matching CloseDir is always the next call at
// that nesting level (once every child it described has itself been
// closed), and likewise for a file between AddFile/OpenFile and
// CloseFile. Every field is optional; a nil one is simply skipped, with
// no error and no other effect -- e.g. leave AddFile/OpenFile/CloseFile
// unset entirely for a caller that only cares about which paths exist,
// not their content.
//
// This intentionally never touches a filesystem, a database, or any
// other storage on its own: it is exactly as low-level as the rest of
// this package's own API (Server's own callback fields work the same
// way), leaving what "checking out" or "updating" actually *means* -- a
// real working copy on disk, an in-memory tree, forwarding straight into
// some other store -- entirely up to the caller. See cmd/go-svn's own
// "checkout"/"update" subcommands for a filesystem-backed implementation
// built purely on this API.
type Editor struct {
	// TargetRev reports the revision this edit brings the caller to (a
	// real svnserve always sends this, as the very first call, before
	// OpenRoot).
	TargetRev func(rev int) error

	// OpenRoot starts the edit. rev, if non-nil, is the root directory's
	// own last-changed revision.
	OpenRoot func(rev *int) error

	// DeleteEntry reports that path, a child of the directory most
	// recently opened via AddDir/OpenDir (or the root, if none is open),
	// no longer exists -- or is about to be replaced by an AddDir/AddFile
	// call that immediately follows. rev, if non-nil, is the revision at
	// which path was determined to be gone.
	DeleteEntry func(path string, rev *int) error

	// AddDir reports a new directory at path, becoming the currently
	// open directory until the matching CloseDir. If copyFrom is
	// non-nil, the directory is a copy of that path at that revision
	// rather than being brand new.
	AddDir func(path string, copyFrom *EditorCopyFrom) error

	// OpenDir is like AddDir, but for a directory the report claimed the
	// caller already has: rev is the revision it's being opened at.
	OpenDir func(path string, rev int) error

	// ChangeDirProp sets property name on the currently open directory
	// to value, or deletes it if value is nil.
	ChangeDirProp func(name string, value []byte) error

	// CloseDir closes the currently open directory.
	CloseDir func() error

	// AbsentDir reports that path, a child of the currently open
	// directory, exists but was deliberately not described further (e.g.
	// because access to it was denied).
	AbsentDir func(path string) error

	// AddFile reports a new file at path, becoming the currently open
	// file until the matching CloseFile. If copyFrom is non-nil, the
	// file is a copy of that path at that revision rather than being
	// brand new. Its content, if any, follows via ApplyTextdelta/
	// CloseFile.
	AddFile func(path string, copyFrom *EditorCopyFrom) error

	// OpenFile is like AddFile, but for a file the report claimed the
	// caller already has: rev is the revision it's being opened at. It
	// must return that file's own current content, since a real svnserve
	// may describe the new content as a real, incremental delta against
	// it rather than a full replacement -- decoding that delta (see
	// ApplyTextdelta) needs the base content to apply it to. Returning
	// the wrong content here (or none, if the server does send a delta
	// referencing a source) makes decoding fail with a "source view out
	// of range"-style error, not silently produce the wrong result. A
	// nil OpenFile field is only safe to leave unset for a report that
	// can never actually open an existing file, e.g. [Client.Checkout]'s
	// own start-empty one.
	OpenFile func(path string, rev int) (baseContent []byte, err error)

	// ApplyTextdelta announces that the currently open file's content is
	// about to be (re)described, via one or more textdelta-chunk
	// commands terminated by textdelta-end -- bookkeeping this package
	// handles entirely on its own, surfaced to the caller only as the
	// final, already-decoded content CloseFile receives. baseChecksum,
	// if non-empty, is the MD5 checksum (as a lowercase hex string)
	// OpenFile's own baseContent is expected to have; it's verified
	// automatically (failing the whole call with an error on a mismatch)
	// rather than left to this field, which exists mainly for
	// observability (e.g. logging).
	ApplyTextdelta func(path string, baseChecksum string) error

	// ChangeFileProp is like ChangeDirProp, but for the currently open
	// file.
	ChangeFileProp func(path string, name string, value []byte) error

	// CloseFile closes the currently open file, with its own final
	// content -- already reassembled and decoded from whatever
	// ApplyTextdelta/textdelta-chunk/textdelta-end described (nil if the
	// file's content was never touched at all, e.g. a copyFrom-only
	// AddFile). The server's own optional checksum for it, if present,
	// is verified automatically against content before this is called.
	CloseFile func(path string, content []byte) error

	// AbsentFile is like AbsentDir, but for a file.
	AbsentFile func(path string) error
}

// pendingFile tracks one file currently open while driveEditor parses an
// incoming Editor Command Set sequence: its own path, the base content
// Editor.OpenFile's own callback returned (nil for a brand new file added
// via "add-file"), the svndiff bytes accumulated so far from
// "textdelta-chunk" calls between "apply-textdelta" and "close-file", and
// whether "apply-textdelta" was ever called at all (a file's content
// need not be resent, e.g. an unmodified copy).
type pendingFile struct {
	path        string
	baseContent []byte
	svndiff     []byte
	touched     bool
}

// reportAndApply drives the report/editor exchange shared by Checkout,
// Update and Diff: sends the given Main Command Set command (cmd/params
// -- "update" for Checkout/Update, "diff" for Diff), a single root
// "set-path" reporting reportRev at startEmpty, then "finish-report", and
// applies the resulting Editor Command Set sequence via driveEditor. It
// returns the revision actually reached (whatever the server's own
// "target-rev" reported). Callers must already hold c.mu.
func (c *Client) reportAndApply(cmd string, params []any, reportRev int, startEmpty bool, editor Editor) (int, error) {
	if err := c.conn.Write([]any{cmd, params}); err != nil {
		return 0, fmt.Errorf("sending %q: %w", cmd, err)
	}
	if err := c.handleAuth(); err != nil {
		return 0, err
	}

	// Report Command Set: a single "set-path" for the report's own root.
	// The wire shape (path, rev, start-empty, lock-tokens, depth),
	// confirmed against a real svnserve, has two more fields beyond what
	// Checkout's, Update's or Diff's own report needs; a fixed
	// "infinity"/no-locks pair matches what a real client sends.
	//
	// reportRev matters even for a start-empty (Checkout) report, whose
	// value is nominally meaningless: confirmed the hard way, sending a
	// hardcoded 0 regardless of the caller's own revision made a checkout
	// anchored below the repository root (but not one anchored at the
	// root itself) fail with a real svnserve's own "160005 Cannot replace
	// a directory from within" -- some part of its internal
	// report/reporter logic evidently uses this value for more than a
	// checkout's own report nominally needs. A real client always
	// resolves and reports its true current revision here (confirmed by
	// capture: it sends "get-latest-rev" before "update" for exactly
	// this, even when the caller didn't ask for a specific revision), so
	// Checkout does the same instead of trying to characterize further
	// what svnserve actually needed it for.
	if err := c.conn.Write([]any{
		"set-path",
		[]any{[]byte(""), reportRev, startEmpty, []any{}, "infinity"},
	}); err != nil {
		return 0, fmt.Errorf("sending \"set-path\": %w", err)
	}
	if err := c.conn.Write([]any{"finish-report", []any{}}); err != nil {
		return 0, fmt.Errorf("sending \"finish-report\": %w", err)
	}
	if err := c.handleAuth(); err != nil {
		return 0, err
	}

	gotRev, aborted, err := driveEditor(&c.conn, editor)
	if err != nil {
		return 0, err
	}
	// driveEditor already acked close-edit/abort-edit; the deferred
	// response to our own earlier "finish-report" is a separate read,
	// since it belongs to finish-report specifically (a different Main
	// Command Set command entirely, e.g. "commit", would owe a
	// differently-shaped deferred response of its own instead -- not
	// driveEditor's concern, since it only reads/dispatches the Editor
	// Command Set itself, not whatever a caller is using it to answer).
	var final Item
	if err := c.conn.ReadResponse(&final); err != nil {
		return 0, fmt.Errorf("reading finish-report's own response: %w", err)
	}
	if aborted {
		return 0, errors.New("aborted by server")
	}
	if gotRev == nil {
		return 0, nil // a real svnserve always sends target-rev, but it's technically optional
	}
	return *gotRev, nil
}

// driveEditor reads and parses an Editor Command Set sequence off cn,
// calling editor's own fields for each command, until "close-edit" or
// "abort-edit" ends it -- acking either the same way a real client does
// (an empty success) before returning. It does not read whatever
// deferred response the Main Command Set command that started the
// exchange in the first place (e.g. "finish-report", "commit") still
// owes the other side once the edit itself is done -- that response's
// own shape is specific to that command, not to the Editor Command Set
// itself, so reading it is the caller's job (see reportAndApply).
// rev is the revision the server's own "target-rev" reported (nil if it
// never sent one), and aborted reports whether the server sent
// "abort-edit" rather than completing normally.
//
// This works the same regardless of which side of the connection cn
// belongs to: parsing an incoming Editor Command Set is direction-
// agnostic, the mirror image of [EditorWriter] (which only ever
// writes one) -- reportAndApply below uses this for a Client parsing
// what a server sends driving a checkout/update/diff, and it's meant to
// be reused the same way for a Server parsing what a client sends
// driving a commit, once this package supports receiving one.
//
// This keeps no token→node map for directories at all: every "add-dir"/
// "open-dir"/"add-file"/"open-file"'s own Path is already the full path
// from the edit's root (confirmed against a real svnserve, and already
// relied on by [EditorWriter] for the reverse direction), so it's passed
// straight through to editor's own callbacks without needing to track
// parent tokens or a currently-open-node stack the way [EditorWriter]
// does for writing this same sequence. A file's own wire token *is*
// tracked internally (see pendingFile), purely to correlate
// "apply-textdelta"/"textdelta-chunk"/"textdelta-end"/"close-file" back
// to the same path, since those commands only ever carry a token, not a
// path -- editor's own callbacks never see a token at all, only the path
// it already resolves to.
func driveEditor(cn *conn, editor Editor) (rev *int, aborted bool, err error) {
	pending := map[string]*pendingFile{}

	for {
		var item Item
		if err := cn.Read(&item); err != nil {
			return rev, false, fmt.Errorf("reading editor command: %w", err)
		}
		var command struct {
			Name   string
			Params Item
		}
		if err := Unmarshal(item, &command); err != nil {
			return rev, false, fmt.Errorf("unmarshaling editor command: %w", err)
		}

		switch command.Name {
		case "target-rev":
			var args struct{ Rev int }
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("target-rev: %w", err)
			}
			rev = &args.Rev
			if editor.TargetRev != nil {
				if err := editor.TargetRev(args.Rev); err != nil {
					return rev, false, err
				}
			}
		case "open-root":
			var args struct{ Rev *int }
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("open-root: %w", err)
			}
			if editor.OpenRoot != nil {
				if err := editor.OpenRoot(args.Rev); err != nil {
					return rev, false, err
				}
			}
		case "delete-entry":
			var args struct {
				Path string
				Rev  *int
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("delete-entry: %w", err)
			}
			if editor.DeleteEntry != nil {
				if err := editor.DeleteEntry(args.Path, args.Rev); err != nil {
					return rev, false, err
				}
			}
		case "add-dir":
			var args struct {
				Path        string
				ParentToken string
				Token       string
				CopyFrom    *EditorCopyFrom
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("add-dir: %w", err)
			}
			if editor.AddDir != nil {
				if err := editor.AddDir(args.Path, args.CopyFrom); err != nil {
					return rev, false, err
				}
			}
		case "open-dir":
			var args struct {
				Path        string
				ParentToken string
				Token       string
				Rev         int
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("open-dir: %w", err)
			}
			if editor.OpenDir != nil {
				if err := editor.OpenDir(args.Path, args.Rev); err != nil {
					return rev, false, err
				}
			}
		case "change-dir-prop":
			var args struct {
				Token string
				Name  string
				Value *string
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("change-dir-prop: %w", err)
			}
			if editor.ChangeDirProp != nil {
				var value []byte
				if args.Value != nil {
					value = []byte(*args.Value)
				}
				if err := editor.ChangeDirProp(args.Name, value); err != nil {
					return rev, false, err
				}
			}
		case "close-dir":
			if editor.CloseDir != nil {
				if err := editor.CloseDir(); err != nil {
					return rev, false, err
				}
			}
		case "absent-dir":
			var args struct{ Path string }
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("absent-dir: %w", err)
			}
			if editor.AbsentDir != nil {
				if err := editor.AbsentDir(args.Path); err != nil {
					return rev, false, err
				}
			}
		case "add-file":
			var args struct {
				Path        string
				ParentToken string
				Token       string
				CopyFrom    *EditorCopyFrom
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("add-file: %w", err)
			}
			pending[args.Token] = &pendingFile{path: args.Path}
			if editor.AddFile != nil {
				if err := editor.AddFile(args.Path, args.CopyFrom); err != nil {
					return rev, false, err
				}
			}
		case "open-file":
			var args struct {
				Path        string
				ParentToken string
				Token       string
				Rev         int
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("open-file: %w", err)
			}
			var base []byte
			if editor.OpenFile != nil {
				b, err := editor.OpenFile(args.Path, args.Rev)
				if err != nil {
					return rev, false, err
				}
				base = b
			}
			pending[args.Token] = &pendingFile{path: args.Path, baseContent: base}
		case "apply-textdelta":
			var args struct {
				Token        string
				BaseChecksum *string
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("apply-textdelta: %w", err)
			}
			pf, ok := pending[args.Token]
			if !ok {
				return rev, false, fmt.Errorf("apply-textdelta: unknown file token %q", args.Token)
			}
			pf.touched = true
			checksum := ""
			if args.BaseChecksum != nil {
				checksum = *args.BaseChecksum
				if got := fmt.Sprintf("%x", md5.Sum(pf.baseContent)); got != checksum {
					return rev, false, fmt.Errorf("apply-textdelta: %s: base checksum mismatch (got %s, want %s) -- the caller's own OpenFile content no longer matches the reported revision", pf.path, got, checksum)
				}
			}
			if editor.ApplyTextdelta != nil {
				if err := editor.ApplyTextdelta(pf.path, checksum); err != nil {
					return rev, false, err
				}
			}
		case "textdelta-chunk":
			var args struct {
				Token string
				Chunk []byte
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("textdelta-chunk: %w", err)
			}
			pf, ok := pending[args.Token]
			if !ok {
				return rev, false, fmt.Errorf("textdelta-chunk: unknown file token %q", args.Token)
			}
			pf.svndiff = append(pf.svndiff, args.Chunk...)
		case "textdelta-end":
			// No-op: decoding happens at "close-file" instead.
		case "change-file-prop":
			var args struct {
				Token string
				Name  string
				Value *string
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("change-file-prop: %w", err)
			}
			pf, ok := pending[args.Token]
			if !ok {
				return rev, false, fmt.Errorf("change-file-prop: unknown file token %q", args.Token)
			}
			if editor.ChangeFileProp != nil {
				var value []byte
				if args.Value != nil {
					value = []byte(*args.Value)
				}
				if err := editor.ChangeFileProp(pf.path, args.Name, value); err != nil {
					return rev, false, err
				}
			}
		case "close-file":
			var args struct {
				Token    string
				Checksum *string
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("close-file: %w", err)
			}
			pf, ok := pending[args.Token]
			if !ok {
				return rev, false, fmt.Errorf("close-file: unknown file token %q", args.Token)
			}
			var content []byte
			if pf.touched {
				decoded, derr := decodeSvndiff(pf.baseContent, pf.svndiff)
				if derr != nil {
					return rev, false, fmt.Errorf("close-file: %s: %w", pf.path, derr)
				}
				content = decoded
			}
			if args.Checksum != nil {
				if got := fmt.Sprintf("%x", md5.Sum(content)); got != *args.Checksum {
					return rev, false, fmt.Errorf("close-file: %s: checksum mismatch (got %s, want %s)", pf.path, got, *args.Checksum)
				}
			}
			if editor.CloseFile != nil {
				if err := editor.CloseFile(pf.path, content); err != nil {
					return rev, false, err
				}
			}
			delete(pending, args.Token)
		case "absent-file":
			var args struct{ Path string }
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("absent-file: %w", err)
			}
			if editor.AbsentFile != nil {
				if err := editor.AbsentFile(args.Path); err != nil {
					return rev, false, err
				}
			}
		case "close-edit":
			if err := cn.WriteSuccess([]any{}); err != nil {
				return rev, false, fmt.Errorf("acking close-edit: %w", err)
			}
			return rev, false, nil
		case "abort-edit":
			if err := cn.WriteSuccess([]any{}); err != nil {
				return rev, false, fmt.Errorf("acking abort-edit: %w", err)
			}
			return rev, true, nil
		default:
			return rev, false, fmt.Errorf("unknown editor command %q", command.Name)
		}
	}
}
