package svn

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// IsSingleRevisionUpdate reports whether report describes a client whose
// entire working copy sits at one uniform revision: a single "set-path"
// for the report's own root (path ""), not start-empty (the client
// already has something). This is the common, non-"mixed-revision"
// shape of a working copy (one where every subtree came from the same
// "svn update", rather than some of it being pinned to an older revision
// with e.g. "svn update -r"), which is as far as [Server.UpdateEdit] and
// [Server.SwitchEdit] go. If ok is true, rev is that uniform revision.
func IsSingleRevisionUpdate(report []ReportedPath) (rev uint, ok bool) {
	if len(report) != 1 || report[0].Path != "" || report[0].StartEmpty {
		return 0, false
	}
	return report[0].Rev, true
}

// UpdateEdit builds the Editor Command Set sequence to bring a client
// already at fromRev (see [IsSingleRevisionUpdate]) up to toRev, for path
// (in the same, session-anchor-relative form s.List/s.GetFile already
// take). Unlike [Server.CheckoutEdit], this walks both revisions of the
// tree and only describes what actually changed: a new node via
// AddDir/AddFile, a removed one via DeleteEntry, and a modified file via
// OpenFile plus a fresh ApplyTextdelta -- an unmodified file is skipped
// entirely, never even opened (confirmed against a real svnserve). Both
// s.List and s.GetFile must be set.
//
// target, if non-empty, restricts this to just the one node reached by
// following target's own path segments down from path, instead of every
// child of path -- this is needed whenever the actual target is (or is
// inside) a plain file: path itself must always be a directory (List's
// own contract requires it), but a real client asking to update or diff
// a single file (e.g. "svn update file.txt", or "svn diff file.txt")
// can't anchor a session at the file directly (the Editor Command Set
// has no way to represent that: open-root always opens a directory).
// Every path segment strictly between path and the target is walked (to
// find the target and know whether it changed) but never itself
// described in the editor sequence -- no open-dir/add-dir, no
// entry-props -- and the target itself is described directly as a child
// of the root, using just its own base name, as if its parent directory
// coincided with the edit's root regardless of how many real path
// segments actually separate them. This matches what a real client
// itself expects (confirmed the hard way): it computes the target's own
// local path by joining every open-dir/open-file name it receives, so
// describing an intermediate directory produces a bogus, doubled path
// (e.g. ".../trunk/trunk/main.go" instead of ".../trunk/main.go") that
// a real client rejects outright once it tries to apply the edit.
//
// What target itself actually contains differs between the two commands
// that set it, confirmed against a real client for each: "update" (see
// Server.Update) always anchors a fresh session exactly at the target's
// own parent directory, so its own target parameter reliably names only
// the target's bare child name (a single path segment, relative to that
// already-anchored session). "diff" (see Server.Diff) instead often
// reuses an existing, possibly much higher-anchored session while still
// reporting target as only that same bare child name -- so a
// Server.FinishReport implementation handling "diff" needs to compute
// the real, possibly multi-segment target itself, typically by comparing
// Server.Diff's own versusURL argument against s.ReposInfo.URL (see
// [RepoRelativePath]) and then stripping its own session anchor's prefix
// from the result, the same way it already resolves every other path
// argument.
//
// The result is ready to return from a [Server.FinishReport]
// implementation, alongside CheckoutEdit for the plain-checkout case:
//
//	server.FinishReport = func(report []svn.ReportedPath) ([]svn.Item, error) {
//		if svn.IsPlainCheckout(report) {
//			return server.CheckoutEdit(report[0].Path, targetRev)
//		}
//		if fromRev, ok := svn.IsSingleRevisionUpdate(report); ok {
//			return server.UpdateEdit(report[0].Path, target, fromRev, targetRev)
//		}
//		return nil, errors.New("only a plain checkout or single-revision update is supported")
//	}
//
// (targetRev and target are whatever the preceding "update"/"diff"
// command asked for, which Server.Update's/Server.Diff's callback is
// responsible for remembering.)
//
// A directory present at both revisions is always visited (with a fresh
// OpenDir and its svn:entry:* properties resent), whether or not anything
// actually changed inside it: unlike a real svnserve, UpdateEdit does not
// try to detect an unmodified subtree without recursing into it, since a
// directory's own CreatedRev only reflects its direct entries changing,
// not something changing further down. This is a real inefficiency for a
// large, mostly-unchanged tree (a real client tolerates the resulting
// empty open-dir/close-dir pairs just fine, but it's still needless
// traffic), left as a known limitation rather than solved preemptively.
func (s *Server) UpdateEdit(path, target string, fromRev, toRev uint) ([]Item, error) {
	return s.diffEdit(path, path, target, fromRev, toRev)
}

// SwitchEdit builds the Editor Command Set sequence to switch a client
// already at fromPath (at fromRev, see [IsSingleRevisionUpdate]) to
// toPath (at toRev), for a "switch" command (see [Server.Switch]). It is
// otherwise identical to [Server.UpdateEdit] -- indeed UpdateEdit is just
// SwitchEdit called with the same path on both sides -- except that
// fromPath and toPath may name two entirely different repository
// locations (e.g. "trunk" and "branches/foo"), corresponding to each
// other purely structurally (by name, at each level) rather than by any
// shared history: a file present under both is compared by content/
// revision exactly as UpdateEdit already does, a file or directory only
// under toPath is added, and one only under fromPath is deleted, with no
// attempt to detect a rename or otherwise use copy ancestry. target has
// the same meaning as UpdateEdit's own (see its doc comment), navigating
// down from both fromPath and toPath in parallel by the same segment
// names.
func (s *Server) SwitchEdit(fromPath, toPath, target string, fromRev, toRev uint) ([]Item, error) {
	return s.diffEdit(fromPath, toPath, target, fromRev, toRev)
}

// diffEdit is the shared implementation behind UpdateEdit and SwitchEdit:
// it diffs fromPath at fromRev against toPath at toRev (the same path in
// UpdateEdit's case, two different ones in SwitchEdit's), restricted to
// target if non-empty (see UpdateEdit's own doc comment).
func (s *Server) diffEdit(fromPath, toPath, target string, fromRev, toRev uint) ([]Item, error) {
	if s.List == nil || s.GetFile == nil {
		return nil, errors.New("svn: diffEdit: Server.List and Server.GetFile must both be set")
	}
	toEntries, err := s.List(toPath, &toRev, "immediates", checkoutListFields, nil)
	if err != nil {
		return nil, err
	}
	self, toChildren := splitCheckoutEntries(toEntries)
	fromEntries, err := s.listOrEmpty(fromPath, fromRev)
	if err != nil {
		return nil, err
	}
	fromSelf, fromChildren := splitCheckoutEntries(fromEntries)

	var toSelfPath, fromSelfPath string
	if self != nil {
		toSelfPath = self.Path
	}
	if fromSelf != nil {
		fromSelfPath = fromSelf.Path
	}

	e := NewEditorWriter()
	if err := e.TargetRev(toRev); err != nil {
		return nil, err
	}
	var rootRev *uint
	if self != nil {
		r := self.CreatedRev
		rootRev = &r
	}
	if err := e.OpenRoot(rootRev); err != nil {
		return nil, err
	}

	if target == "" {
		if self != nil {
			if err := s.checkoutEmitEntryProps(e.ChangeDirProp, *self); err != nil {
				return nil, err
			}
		}
		if err := s.updateChildren(e, fromPath, toPath, "", fromSelfPath, toSelfPath, fromChildren, toChildren, fromRev, toRev); err != nil {
			return nil, err
		}
	} else {
		segments := strings.Split(target, "/")
		if err := s.updateNavigateToTarget(e, fromPath, toPath, fromSelfPath, toSelfPath, fromChildren, toChildren, segments, fromRev, toRev); err != nil {
			return nil, err
		}
	}
	if err := e.CloseDir(); err != nil {
		return nil, err
	}
	return e.Items()
}

// findChild returns the one entry of children whose name (relative to
// selfPath, the directory they are children of) is name, and whether one
// was found at all.
func findChild(children []Dirent, selfPath, name string) (Dirent, bool) {
	for _, c := range children {
		if childName(c, selfPath) == name {
			return c, true
		}
	}
	return Dirent{}, false
}

// listOrEmpty is like s.List, but treats a "path does not exist at rev"
// error as an empty listing rather than failing -- used for the "from"
// side of a diff, where the whole subtree may simply not have existed
// yet at fromRev (or, for SwitchEdit, may not exist at fromPath at all).
func (s *Server) listOrEmpty(path string, rev uint) ([]Dirent, error) {
	entries, err := s.List(path, &rev, "immediates", checkoutListFields, nil)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// updateChildren describes, under e's currently open directory, the
// difference between fromChildren and toChildren (the already-listed
// direct children of fromDirPath at fromRev and of toDirPath at toRev
// respectively, whose own Path is fromSelfPath and toSelfPath
// respectively): removed or kind-changed entries first, as a real
// svnserve orders them, then every entry still present at toRev (new,
// modified, unchanged-but-visited, or recursed into). fromDirPath and
// toDirPath are equal for a plain update (see UpdateEdit) but may be
// entirely different repository locations for a switch (see
// SwitchEdit), corresponding to each other purely by name. wirePath is
// toDirPath's equivalent relative to the edit's own root; see
// EditorWriter's doc comment on why every Editor Command Set path must
// be in that form, not toDirPath's own (session-anchor-relative) form.
func (s *Server) updateChildren(e *EditorWriter, fromDirPath, toDirPath, wirePath, fromSelfPath, toSelfPath string, fromChildren, toChildren []Dirent, fromRev, toRev uint) error {
	fromByName := make(map[string]Dirent, len(fromChildren))
	for _, entry := range fromChildren {
		fromByName[childName(entry, fromSelfPath)] = entry
	}
	toByName := make(map[string]Dirent, len(toChildren))
	for _, entry := range toChildren {
		toByName[childName(entry, toSelfPath)] = entry
	}

	for name, from := range fromByName {
		if name == "" {
			continue
		}
		if to, stillThere := toByName[name]; stillThere && to.Kind == from.Kind {
			continue
		}
		if err := e.DeleteEntry(joinNonEmpty(wirePath, name), &toRev); err != nil {
			return err
		}
	}

	for _, to := range toChildren {
		name := childName(to, toSelfPath)
		if name == "" {
			continue
		}
		childFromDirPath := joinNonEmpty(fromDirPath, name)
		childToDirPath := joinNonEmpty(toDirPath, name)
		childWirePath := joinNonEmpty(wirePath, name)
		from, existed := fromByName[name]
		isNew := !existed || from.Kind != to.Kind

		switch to.Kind {
		case "dir":
			if isNew {
				if err := e.AddDir(childWirePath, nil); err != nil {
					return err
				}
			} else if err := e.OpenDir(childWirePath, fromRev); err != nil {
				return err
			}
			if err := s.checkoutEmitEntryProps(e.ChangeDirProp, to); err != nil {
				return err
			}
			toGrandEntries, err := s.List(childToDirPath, &toRev, "immediates", checkoutListFields, nil)
			if err != nil {
				return err
			}
			// to.Path (not a fresh self-lookup) is already this child's
			// own true, repository-root-relative Path, per Server.List's
			// contract -- reused directly as the next level's toSelfPath.
			_, toGrandchildren := splitCheckoutEntries(toGrandEntries)
			var fromGrandchildren []Dirent
			var fromGrandSelfPath string
			if !isNew {
				fromGrandEntries, err := s.listOrEmpty(childFromDirPath, fromRev)
				if err != nil {
					return err
				}
				var fromGrandSelf *Dirent
				fromGrandSelf, fromGrandchildren = splitCheckoutEntries(fromGrandEntries)
				if fromGrandSelf != nil {
					fromGrandSelfPath = fromGrandSelf.Path
				}
			}
			if err := s.updateChildren(e, childFromDirPath, childToDirPath, childWirePath, fromGrandSelfPath, to.Path, fromGrandchildren, toGrandchildren, fromRev, toRev); err != nil {
				return err
			}
			if err := e.CloseDir(); err != nil {
				return err
			}
		case "file":
			switch {
			case isNew:
				if err := s.checkoutAddFile(e, childToDirPath, childWirePath, to, toRev); err != nil {
					return err
				}
			case to.CreatedRev == from.CreatedRev:
				// Unchanged: a real svnserve skips it entirely, never
				// even opening it.
			default:
				if err := s.updateOpenFile(e, childToDirPath, childWirePath, to, fromRev, toRev); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("svn: diffEdit: %s: unsupported node kind %q", childToDirPath, to.Kind)
		}
	}
	return nil
}

// updateNavigateToTarget finds the node reached by following segments
// down from fromDirPath/toDirPath in parallel (already listed as
// fromChildren/toChildren, direct children of fromDirPath/toDirPath whose
// own Path is fromSelfPath/toSelfPath), and describes only that final
// node, directly as a child of e's currently open directory (the root)
// -- using just its own base name as the wire path, not the full path
// from toDirPath. Every directory strictly between toDirPath and the
// target is walked, to find the target and know whether it changed, but
// is never itself described (no open-dir/add-dir, no entry-props); see
// UpdateEdit's own doc comment for why.
func (s *Server) updateNavigateToTarget(e *EditorWriter, fromDirPath, toDirPath, fromSelfPath, toSelfPath string, fromChildren, toChildren []Dirent, segments []string, fromRev, toRev uint) error {
	name := segments[0]
	to, toFound := findChild(toChildren, toSelfPath, name)
	from, fromFound := findChild(fromChildren, fromSelfPath, name)

	if !toFound {
		if fromFound {
			return e.DeleteEntry(name, &toRev)
		}
		return nil // never existed at either revision/location: nothing to say
	}

	childFromDirPath := joinNonEmpty(fromDirPath, name)
	childToDirPath := joinNonEmpty(toDirPath, name)
	isNew := !fromFound || from.Kind != to.Kind

	if len(segments) > 1 {
		if to.Kind != "dir" {
			return fmt.Errorf("svn: diffEdit: %s: not a directory", childToDirPath)
		}
		toGrandEntries, err := s.List(childToDirPath, &toRev, "immediates", checkoutListFields, nil)
		if err != nil {
			return err
		}
		_, toGrandchildren := splitCheckoutEntries(toGrandEntries)
		var fromGrandchildren []Dirent
		var fromGrandSelfPath string
		if !isNew {
			fromGrandEntries, err := s.listOrEmpty(childFromDirPath, fromRev)
			if err != nil {
				return err
			}
			var fromGrandSelf *Dirent
			fromGrandSelf, fromGrandchildren = splitCheckoutEntries(fromGrandEntries)
			if fromGrandSelf != nil {
				fromGrandSelfPath = fromGrandSelf.Path
			}
		}
		return s.updateNavigateToTarget(e, childFromDirPath, childToDirPath, fromGrandSelfPath, to.Path, fromGrandchildren, toGrandchildren, segments[1:], fromRev, toRev)
	}

	// The target itself: describe it as a direct child of the root,
	// using just its own name -- if it's a directory, everything inside
	// it is then described normally (updateChildren), with wirePath
	// starting fresh at that same bare name.
	switch to.Kind {
	case "dir":
		if isNew {
			if err := e.AddDir(name, nil); err != nil {
				return err
			}
		} else if err := e.OpenDir(name, fromRev); err != nil {
			return err
		}
		if err := s.checkoutEmitEntryProps(e.ChangeDirProp, to); err != nil {
			return err
		}
		toGrandEntries, err := s.List(childToDirPath, &toRev, "immediates", checkoutListFields, nil)
		if err != nil {
			return err
		}
		_, toGrandchildren := splitCheckoutEntries(toGrandEntries)
		var fromGrandchildren []Dirent
		var fromGrandSelfPath string
		if !isNew {
			fromGrandEntries, err := s.listOrEmpty(childFromDirPath, fromRev)
			if err != nil {
				return err
			}
			var fromGrandSelf *Dirent
			fromGrandSelf, fromGrandchildren = splitCheckoutEntries(fromGrandEntries)
			if fromGrandSelf != nil {
				fromGrandSelfPath = fromGrandSelf.Path
			}
		}
		if err := s.updateChildren(e, childFromDirPath, childToDirPath, name, fromGrandSelfPath, to.Path, fromGrandchildren, toGrandchildren, fromRev, toRev); err != nil {
			return err
		}
		return e.CloseDir()
	case "file":
		switch {
		case isNew:
			return s.checkoutAddFile(e, childToDirPath, name, to, toRev)
		case to.CreatedRev == from.CreatedRev:
			return nil // unchanged: skip it entirely, like updateChildren does
		default:
			return s.updateOpenFile(e, childToDirPath, name, to, fromRev, toRev)
		}
	default:
		return fmt.Errorf("svn: diffEdit: %s: unsupported node kind %q", childToDirPath, to.Kind)
	}
}

// joinNonEmpty joins a and b with "/", except that either being "" just
// yields the other.
func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "/" + b
	}
}

// updateOpenFile describes a modified, already-existing file: an
// OpenFile (not AddFile) at fromRev, followed by its (resent, since a
// real svnserve does the same even for a file it isn't adding) entry
// properties, its new content, and a close with the new checksum.
// dirPath (the file's new location, toDirPath in the caller's terms) is
// what its content is read from, at toRev -- fromRev is only used as the
// wire revision OpenFile announces as the client's own baseline.
func (s *Server) updateOpenFile(e *EditorWriter, dirPath, wirePath string, entry Dirent, fromRev, toRev uint) error {
	_, _, content, err := s.GetFile(dirPath, &toRev, false, true)
	if err != nil {
		return err
	}
	if err := e.OpenFile(wirePath, fromRev); err != nil {
		return err
	}
	if err := s.checkoutEmitEntryProps(e.ChangeFileProp, entry); err != nil {
		return err
	}
	if err := e.ApplyTextdelta(content, nil); err != nil {
		return err
	}
	checksum := []byte(fmt.Sprintf("%x", md5.Sum(content)))
	return e.CloseFile(checksum)
}
