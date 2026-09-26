package svn

import (
	"crypto/md5"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// IsPlainCheckout reports whether report -- the accumulated result of a
// report/editor exchange, as passed to [Server.FinishReport] -- describes
// exactly the shape a checkout's client report always has: a single
// "set-path" for the report's own root (path ""), with start-empty true
// (the client has nothing at all yet), and nothing else. Any other shape
// (more than one entry, a non-root path, or an existing revision the
// client already has) means the client is describing a real "update"
// against something it already has, which needs actual tree-diffing
// logic [CheckoutEdit] does not implement.
func IsPlainCheckout(report []ReportedPath) bool {
	return len(report) == 1 && report[0].Path == "" && report[0].StartEmpty
}

// checkoutListFields are the Server.List fields CheckoutEdit needs for
// every entry: enough to describe each node's content (Kind) and to seed
// the svn:entry:* pseudo-properties below.
var checkoutListFields = []string{"kind", "created-rev", "time", "last-author"}

// CheckoutEdit builds the Editor Command Set sequence for a checkout of
// path (in the same, session-anchor-relative form s.List and s.GetFile
// already take) at rev: since a checkout's client report always reduces
// to "I have nothing" (see [IsPlainCheckout]), there is nothing to diff
// against, so every node under path at rev is simply described as newly
// added. Both s.List and s.GetFile must be set. The result is ready to
// return from a [Server.FinishReport] implementation, e.g.:
//
//	server.FinishReport = func(report []svn.ReportedPath) ([]svn.Item, error) {
//		if !svn.IsPlainCheckout(report) {
//			return nil, errors.New("only a plain checkout is supported")
//		}
//		return server.CheckoutEdit(report[0].Path, targetRev)
//	}
//
// (targetRev is whatever revision the preceding "update" command asked
// for, which Server.Update's callback is responsible for remembering --
// FinishReport is not told it again.)
//
// Every node also gets its svn:entry:committed-rev/committed-date/
// last-author/uuid pseudo-properties set, from the same Dirent.CreatedRev/
// CreatedDate/LastAuthor/s.ReposInfo.UUID a real svnserve uses for this --
// a real svn client's local metadata database asserts on a missing
// committed-rev (confirmed: omitting it crashes "svn checkout" outright,
// not just leaving stale metadata), so this isn't optional polish.
func (s *Server) CheckoutEdit(path string, rev uint) ([]Item, error) {
	if s.List == nil || s.GetFile == nil {
		return nil, errors.New("svn: CheckoutEdit: Server.List and Server.GetFile must both be set")
	}
	entries, err := s.List(path, &rev, "immediates", checkoutListFields, nil)
	if err != nil {
		return nil, err
	}
	self, children := splitCheckoutEntries(entries)

	e := NewEditorWriter()
	if err := e.TargetRev(rev); err != nil {
		return nil, err
	}
	var rootRev *uint
	var selfPath string
	if self != nil {
		r := self.CreatedRev
		rootRev = &r
		selfPath = self.Path
	}
	if err := e.OpenRoot(rootRev); err != nil {
		return nil, err
	}
	if self != nil {
		if err := s.checkoutEmitEntryProps(e.ChangeDirProp, *self); err != nil {
			return nil, err
		}
	}
	if err := s.checkoutAddChildren(e, path, "", selfPath, children, rev); err != nil {
		return nil, err
	}
	if err := e.CloseDir(); err != nil {
		return nil, err
	}
	return e.Items()
}

// splitCheckoutEntries separates a Server.List result into the queried
// directory's own entry (always present, per Server.List's documented
// contract) and its direct children. The self-entry is identified as the
// one whose Path is the (necessarily unique) shortest among all the
// entries, rather than assuming it equals "/" + whatever path was
// queried with: Server.List's Dirent.Path is always the full,
// repository-root-relative path (confirmed against a real svnserve),
// which only coincides with "/" + the queried path when the session
// isn't anchored below the repository root -- for an anchored session
// (e.g. a checkout of ".../repo/trunk"), the queried path is
// session-anchor-relative ("" for the checkout's own root) while every
// Dirent.Path returned for it is still repository-root-relative
// ("/trunk", "/trunk/main.go", ...), so the two only agree by
// coincidence. Every child's Path is exactly the self-entry's own Path
// plus "/" plus its own name, so the self-entry -- and only the
// self-entry -- is a strict prefix of every other entry's Path, and so
// has the shortest one (confirmed the hard way: an earlier version of
// this comparison caused "E210004: Malformed network data" against a
// real svn client checking out a repository subdirectory).
func splitCheckoutEntries(entries []Dirent) (self *Dirent, children []Dirent) {
	if len(entries) == 0 {
		return nil, nil
	}
	selfIdx := 0
	for i := 1; i < len(entries); i++ {
		if len(entries[i].Path) < len(entries[selfIdx].Path) {
			selfIdx = i
		}
	}
	e := entries[selfIdx]
	self = &e
	for i, entry := range entries {
		if i != selfIdx {
			children = append(children, entry)
		}
	}
	return self, children
}

// childName returns entry's name relative to selfPath (the Path of the
// directory it was listed as a child of, from splitCheckoutEntries),
// by stripping that exact prefix -- unlike the session-anchor-relative
// path originally passed to Server.List, selfPath is guaranteed to be a
// real, repository-root-relative prefix of every child's own Path.
func childName(entry Dirent, selfPath string) string {
	name := strings.TrimPrefix(entry.Path, selfPath)
	return strings.TrimPrefix(name, "/")
}

// checkoutEmitEntryProps sets the svn:entry:* pseudo-properties a real
// svnserve always sends for a node it is describing as part of a
// checkout, using setProp (e.ChangeDirProp or e.ChangeFileProp, whichever
// matches entry's own kind). committed-date and last-author are skipped
// if entry didn't have them (e.g. a List implementation that did not
// populate them), but committed-rev and uuid are always sent, since a
// real client's local metadata database requires the former outright.
func (s *Server) checkoutEmitEntryProps(setProp func(name string, value []byte) error, entry Dirent) error {
	if err := setProp("svn:entry:committed-rev", []byte(strconv.FormatUint(uint64(entry.CreatedRev), 10))); err != nil {
		return err
	}
	if entry.CreatedDate != "" {
		if err := setProp("svn:entry:committed-date", []byte(entry.CreatedDate)); err != nil {
			return err
		}
	}
	if entry.LastAuthor != "" {
		if err := setProp("svn:entry:last-author", []byte(entry.LastAuthor)); err != nil {
			return err
		}
	}
	if s.ReposInfo.UUID != "" {
		if err := setProp("svn:entry:uuid", []byte(s.ReposInfo.UUID)); err != nil {
			return err
		}
	}
	return nil
}

// checkoutAddChildren adds every entry in children (the already-listed
// direct children of dirPath, whose own Path is selfPath) as a node
// under e's currently open directory, recursing into subdirectories with
// a fresh Server.List call each (to discover their own children -- a
// child's own metadata, unlike its children's, is already in hand from
// the listing that produced it, so describing the child itself costs no
// extra round trip). wirePath is dirPath's equivalent relative to the
// edit's own root; see EditorWriter's doc comment on why every Editor
// Command Set path must be in that form, not dirPath's own
// (session-anchor-relative) form.
func (s *Server) checkoutAddChildren(e *EditorWriter, dirPath, wirePath, selfPath string, children []Dirent, rev uint) error {
	for _, entry := range children {
		name := childName(entry, selfPath)
		if name == "" {
			continue
		}
		childDirPath, childWirePath := name, name
		if dirPath != "" {
			childDirPath = dirPath + "/" + name
		}
		if wirePath != "" {
			childWirePath = wirePath + "/" + name
		}
		switch entry.Kind {
		case "dir":
			if err := e.AddDir(childWirePath, nil); err != nil {
				return err
			}
			if err := s.checkoutEmitEntryProps(e.ChangeDirProp, entry); err != nil {
				return err
			}
			grandEntries, err := s.List(childDirPath, &rev, "immediates", checkoutListFields, nil)
			if err != nil {
				return err
			}
			// entry.Path (not a fresh self-lookup) is already this
			// child's own true, repository-root-relative Path, per
			// Server.List's contract -- reused directly as the next
			// level's selfPath.
			_, grandchildren := splitCheckoutEntries(grandEntries)
			if err := s.checkoutAddChildren(e, childDirPath, childWirePath, entry.Path, grandchildren, rev); err != nil {
				return err
			}
			if err := e.CloseDir(); err != nil {
				return err
			}
		case "file":
			if err := s.checkoutAddFile(e, childDirPath, childWirePath, entry, rev); err != nil {
				return err
			}
		default:
			return fmt.Errorf("svn: CheckoutEdit: %s: unsupported node kind %q", childDirPath, entry.Kind)
		}
	}
	return nil
}

func (s *Server) checkoutAddFile(e *EditorWriter, dirPath, wirePath string, entry Dirent, rev uint) error {
	_, _, content, err := s.GetFile(dirPath, &rev, false, true)
	if err != nil {
		return err
	}
	if err := e.AddFile(wirePath, nil); err != nil {
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
