package svn

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkoutFile tracks one file currently open while Checkout applies an
// incoming Editor Command Set sequence: its own local destination path,
// and the svndiff bytes accumulated so far from "textdelta-chunk" calls
// between "apply-textdelta" and "close-file".
type checkoutFile struct {
	localPath string
	svndiff   []byte
}

// Checkout sends an "update" command for whatever Connect/NewClient was
// given (exactly like Stat/List/GetFile's own "" path argument -- there
// is no separate path argument here at all, for the reason given below)
// at rev (nil meaning the latest revision), reporting -- via the Report
// Command Set -- that the client has nothing at all yet (the same
// "start-empty" shape [IsPlainCheckout] recognizes on the server side),
// then applies the resulting Editor Command Set sequence by creating
// destDir (which must not already exist) and writing every file/
// directory it describes underneath it, exactly as "svn checkout" would.
// It returns the revision actually checked out (whatever the server's
// own "target-rev" reported).
//
// To check out just a repository subdirectory, Connect (or NewClient) to
// that subdirectory's own URL directly, the same way [Client.GetFile]'s
// own doc comment already assumes for a session anchored below the
// repository root. Checkout deliberately has no separate "path within
// the repository" parameter that would instead send it as the "update"
// command's own target: real "svn checkout URL/trunk" always anchors a
// fresh session at "trunk" itself and updates with an empty target
// (confirmed against a real svnserve), which is what makes "trunk"
// itself become the checkout's own root -- a non-empty target instead
// describes the target as a *named child* of the edit's root (see
// UpdateEdit's own doc comment on its target parameter), which is the
// right shape for updating one subdirectory of an *already checked-out*
// working copy in place (keeping its own "trunk/..." nesting on disk),
// but the wrong one for a fresh, standalone checkout of just that
// subdirectory -- confirmed the hard way: an earlier version of this
// method took a path parameter and sent it as target, which checked out
// "trunk"'s own content nested one level too deep, under destDir/trunk/
// instead of directly under destDir.
//
// Unlike every other Client method, Checkout does not retry on a broken
// connection: reconnecting and resending "update" from scratch would
// leave whatever was already written under destDir in an inconsistent
// state, and destDir's own creation (which fails if it already exists)
// can't simply be redone.
//
// No working-copy metadata (a ".svn" directory, or anything else a real
// client would use to later run "svn update"/"svn status" against the
// result) is created -- this writes exactly the same kind of plain,
// unversioned tree [Client] export already does, just by driving the
// report/editor exchange to get there instead of a recursive List/
// GetFile walk. That exchange is what makes this the foundation for a
// future Update/Diff, though: this is the first thing in this package
// that parses an incoming Editor Command Set at all, a capability
// [EditorWriter] only ever produces one for the server side of.
//
// Only a plain, brand-new checkout is supported: properties (including
// the svn:entry:* pseudo-properties a real checkout's own working copy
// metadata would need) are read off the wire but discarded, matching
// export's own simplification.
func (c *Client) Checkout(wantRev *int, destDir string) (checkedOutRev int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Resolved once, up front, purely for "set-path"'s own rev field
	// below -- see its own doc comment for why this is needed at all.
	reportRev := 0
	if wantRev != nil {
		reportRev = *wantRev
	} else {
		latest, err := sendCommand[int](c, "get-latest-rev", []any{})
		if err != nil {
			return 0, fmt.Errorf("svn: checkout: %w", err)
		}
		reportRev = latest
	}

	lrev := []int{}
	if wantRev != nil {
		lrev = append(lrev, *wantRev)
	}
	if err := c.conn.Write([]any{
		"update",
		[]any{lrev, []byte(""), true},
	}); err != nil {
		return 0, fmt.Errorf("svn: checkout: sending \"update\": %w", err)
	}
	if err := c.handleAuth(); err != nil {
		return 0, fmt.Errorf("svn: checkout: %w", err)
	}

	// Report Command Set: a single "set-path" for the report's own root,
	// start-empty (the client has nothing yet) -- the same shape a real
	// "svn checkout" always reports, and the only one [IsPlainCheckout]
	// recognizes. The wire shape (path, rev, start-empty, lock-tokens,
	// depth), confirmed against a real svnserve, has two more fields
	// beyond what a plain checkout's own report needs; a fixed
	// "infinity"/no-locks pair matches what a real client sends.
	//
	// reportRev (resolved above, before "update", if wantRev is nil)
	// matters even though start-empty makes its value nominally
	// meaningless: confirmed the hard way, sending a hardcoded 0
	// regardless of wantRev made a checkout anchored below the
	// repository root (but not one anchored at the root itself) fail
	// with a real svnserve's own "160005 Cannot replace a directory from
	// within" -- some part of its internal report/reporter logic
	// evidently uses this value for more than a checkout's own report
	// nominally needs. A real client always resolves and reports its
	// true current revision here (confirmed by capture: it sends
	// "get-latest-rev" before "update" for exactly this, even when the
	// caller didn't ask for a specific revision), so this does the same
	// instead of trying to characterize further what svnserve actually
	// needed it for.
	if err := c.conn.Write([]any{
		"set-path",
		[]any{[]byte(""), reportRev, true, []any{}, "infinity"},
	}); err != nil {
		return 0, fmt.Errorf("svn: checkout: sending \"set-path\": %w", err)
	}
	if err := c.conn.Write([]any{"finish-report", []any{}}); err != nil {
		return 0, fmt.Errorf("svn: checkout: sending \"finish-report\": %w", err)
	}
	if err := c.handleAuth(); err != nil {
		return 0, fmt.Errorf("svn: checkout: %w", err)
	}

	if err := os.Mkdir(destDir, 0o755); err != nil {
		return 0, fmt.Errorf("svn: checkout: %w", err)
	}

	gotRev, aborted, err := c.applyEditor(destDir)
	if err != nil {
		return 0, fmt.Errorf("svn: checkout: %w", err)
	}
	if aborted {
		return 0, fmt.Errorf("svn: checkout: aborted by server")
	}
	if gotRev == nil {
		return 0, nil // a real svnserve always sends target-rev, but it's technically optional
	}
	return *gotRev, nil
}

// applyEditor reads and applies the Editor Command Set sequence a
// "finish-report" response streams (after Checkout's own report/editor
// exchange has already gotten this far), creating files and directories
// under destDir, until "close-edit" or "abort-edit" ends it -- acking
// either the same way a real client does, then reading the final
// response to "finish-report" itself, exactly mirroring what Serve's own
// "finish-report" handling expects back (see handleFinishReport in
// server.go). rev is the revision the server's own "target-rev" reported
// (nil if it never sent one), and aborted reports whether the server
// sent "abort-edit" rather than completing normally.
func (c *Client) applyEditor(destDir string) (rev *int, aborted bool, err error) {
	pending := map[string]*checkoutFile{}

	for {
		var item Item
		if err := c.conn.Read(&item); err != nil {
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
		case "open-root":
			// Nothing to do: destDir already exists, and every later
			// add-dir/add-file's own Path is already the full path from
			// the edit's root, so there's no need to track the root's
			// own token.
		case "add-dir", "open-dir":
			var args struct{ Path string }
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("%s: %w", command.Name, err)
			}
			local, err := safeLocalPath(destDir, args.Path)
			if err != nil {
				return rev, false, err
			}
			if err := os.MkdirAll(local, 0o755); err != nil {
				return rev, false, fmt.Errorf("%s: %w", command.Name, err)
			}
		case "delete-entry":
			var args struct{ Path string }
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("delete-entry: %w", err)
			}
			local, err := safeLocalPath(destDir, args.Path)
			if err != nil {
				return rev, false, err
			}
			if err := os.RemoveAll(local); err != nil {
				return rev, false, fmt.Errorf("delete-entry: %w", err)
			}
		case "add-file", "open-file":
			var args struct {
				Path        string
				ParentToken string
				Token       string
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("%s: %w", command.Name, err)
			}
			local, err := safeLocalPath(destDir, args.Path)
			if err != nil {
				return rev, false, err
			}
			pending[args.Token] = &checkoutFile{localPath: local}
		case "apply-textdelta":
			var args struct{ Token string }
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("apply-textdelta: %w", err)
			}
			if _, ok := pending[args.Token]; !ok {
				return rev, false, fmt.Errorf("apply-textdelta: unknown file token %q", args.Token)
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
		case "close-file":
			var args struct {
				Token    string
				Checksum string
			}
			if err := Unmarshal(command.Params, &args); err != nil {
				return rev, false, fmt.Errorf("close-file: %w", err)
			}
			pf, ok := pending[args.Token]
			if !ok {
				return rev, false, fmt.Errorf("close-file: unknown file token %q", args.Token)
			}
			content, err := decodeSvndiff(nil, pf.svndiff)
			if err != nil {
				return rev, false, fmt.Errorf("close-file: %s: %w", pf.localPath, err)
			}
			if args.Checksum != "" {
				if got := fmt.Sprintf("%x", md5.Sum(content)); got != args.Checksum {
					return rev, false, fmt.Errorf("close-file: %s: checksum mismatch (got %s, want %s)", pf.localPath, got, args.Checksum)
				}
			}
			if err := os.WriteFile(pf.localPath, content, 0o644); err != nil {
				return rev, false, fmt.Errorf("close-file: %w", err)
			}
			delete(pending, args.Token)
		case "change-dir-prop", "change-file-prop", "absent-dir", "absent-file", "textdelta-end", "close-dir":
			// Not modeled: properties (including the svn:entry:*
			// pseudo-properties a real working copy's metadata would
			// need) are read off the wire but discarded, matching
			// export's own simplification -- see Checkout's own doc
			// comment. "absent-dir"/"absent-file" need no local action
			// either, the same way this package's Server side already
			// simplifies them away. "close-dir" needs none here either:
			// unlike EditorWriter, this reader keeps no stack of
			// currently open directories to pop, since every add-dir/
			// add-file's own Path is already the full path from the
			// edit's root (see the "open-root" case above).
		case "close-edit":
			if err := c.conn.WriteSuccess([]any{}); err != nil {
				return rev, false, fmt.Errorf("acking close-edit: %w", err)
			}
			var final Item
			if err := c.conn.ReadResponse(&final); err != nil {
				return rev, false, fmt.Errorf("reading finish-report's own response: %w", err)
			}
			return rev, false, nil
		case "abort-edit":
			if err := c.conn.WriteSuccess([]any{}); err != nil {
				return rev, false, fmt.Errorf("acking abort-edit: %w", err)
			}
			var final Item
			if err := c.conn.ReadResponse(&final); err != nil {
				return rev, false, fmt.Errorf("reading finish-report's own response: %w", err)
			}
			return rev, true, nil
		default:
			return rev, false, fmt.Errorf("unknown editor command %q", command.Name)
		}
	}
}

// safeLocalPath joins destDir with wirePath (a "/"-separated path from
// the edit's own root, e.g. "trunk/main.go", as every Editor Command Set
// path argument is -- see [EditorWriter]'s own doc comment), rejecting a
// path that would escape destDir. This is defensive: a real svnserve is
// not expected to send one, but nothing stops a broken or malicious
// server from trying.
func safeLocalPath(destDir, wirePath string) (string, error) {
	for _, seg := range strings.Split(wirePath, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("svn: checkout: unsafe path %q in editor command", wirePath)
		}
	}
	return filepath.Join(destDir, filepath.FromSlash(wirePath)), nil
}
