package svn

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io/fs"
)

// IsSingleRevisionUpdate reports whether report describes a client whose
// entire working copy sits at one uniform revision: a single "set-path"
// for the report's own root (path ""), not start-empty (the client
// already has something). This is the common, non-"mixed-revision"
// shape of a working copy (one where every subtree came from the same
// "svn update", rather than some of it being pinned to an older revision
// with e.g. "svn update -r"), which is as far as [UpdateEdit] goes. If ok
// is true, rev is that uniform revision.
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
// s.List and s.GetFile must be set. The result is ready to return from a
// [Server.FinishReport] implementation, alongside CheckoutEdit for the
// plain-checkout case:
//
//	server.FinishReport = func(report []svn.ReportedPath) ([]svn.Item, error) {
//		if svn.IsPlainCheckout(report) {
//			return server.CheckoutEdit(report[0].Path, targetRev)
//		}
//		if fromRev, ok := svn.IsSingleRevisionUpdate(report); ok {
//			return server.UpdateEdit(report[0].Path, fromRev, targetRev)
//		}
//		return nil, errors.New("only a plain checkout or single-revision update is supported")
//	}
//
// (targetRev is whatever revision the preceding "update" command asked
// for, which Server.Update's callback is responsible for remembering.)
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
func (s *Server) UpdateEdit(path string, fromRev, toRev uint) ([]Item, error) {
	if s.List == nil || s.GetFile == nil {
		return nil, errors.New("svn: UpdateEdit: Server.List and Server.GetFile must both be set")
	}
	toEntries, err := s.List(path, &toRev, "immediates", checkoutListFields, nil)
	if err != nil {
		return nil, err
	}
	self, toChildren := splitCheckoutEntries(toEntries, path)
	fromEntries, err := s.listOrEmpty(path, fromRev)
	if err != nil {
		return nil, err
	}
	_, fromChildren := splitCheckoutEntries(fromEntries, path)

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
	if self != nil {
		if err := s.checkoutEmitEntryProps(e.ChangeDirProp, *self); err != nil {
			return nil, err
		}
	}
	if err := s.updateChildren(e, path, "", fromChildren, toChildren, fromRev, toRev); err != nil {
		return nil, err
	}
	if err := e.CloseDir(); err != nil {
		return nil, err
	}
	return e.Items()
}

// listOrEmpty is like s.List, but treats a "path does not exist at rev"
// error as an empty listing rather than failing -- used for the "from"
// side of a diff, where the whole subtree may simply not have existed
// yet at fromRev.
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
// direct children of dirPath at fromRev and toRev respectively): removed
// or kind-changed entries first, as a real svnserve orders them, then
// every entry still present at toRev (new, modified, unchanged-but-
// visited, or recursed into). wirePath is dirPath's equivalent relative
// to the edit's own root; see EditorWriter's doc comment on why every
// Editor Command Set path must be in that form, not dirPath's own
// (session-anchor-relative) form.
func (s *Server) updateChildren(e *EditorWriter, dirPath, wirePath string, fromChildren, toChildren []Dirent, fromRev, toRev uint) error {
	fromByName := make(map[string]Dirent, len(fromChildren))
	for _, entry := range fromChildren {
		fromByName[childName(entry, dirPath)] = entry
	}
	toByName := make(map[string]Dirent, len(toChildren))
	for _, entry := range toChildren {
		toByName[childName(entry, dirPath)] = entry
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
		name := childName(to, dirPath)
		if name == "" {
			continue
		}
		childDirPath := joinNonEmpty(dirPath, name)
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
			toGrandEntries, err := s.List(childDirPath, &toRev, "immediates", checkoutListFields, nil)
			if err != nil {
				return err
			}
			_, toGrandchildren := splitCheckoutEntries(toGrandEntries, childDirPath)
			var fromGrandchildren []Dirent
			if !isNew {
				fromGrandEntries, err := s.listOrEmpty(childDirPath, fromRev)
				if err != nil {
					return err
				}
				_, fromGrandchildren = splitCheckoutEntries(fromGrandEntries, childDirPath)
			}
			if err := s.updateChildren(e, childDirPath, childWirePath, fromGrandchildren, toGrandchildren, fromRev, toRev); err != nil {
				return err
			}
			if err := e.CloseDir(); err != nil {
				return err
			}
		case "file":
			switch {
			case isNew:
				if err := s.checkoutAddFile(e, childDirPath, childWirePath, to, toRev); err != nil {
					return err
				}
			case to.CreatedRev == from.CreatedRev:
				// Unchanged: a real svnserve skips it entirely, never
				// even opening it.
			default:
				if err := s.updateOpenFile(e, childDirPath, childWirePath, to, fromRev, toRev); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("svn: UpdateEdit: %s: unsupported node kind %q", childDirPath, to.Kind)
		}
	}
	return nil
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
