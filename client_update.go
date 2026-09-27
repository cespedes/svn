package svn

import "fmt"

// Update sends an "update" command for whatever Connect/NewClient was
// given (see [Client.Checkout]'s own doc comment for why there is no
// separate path argument), reporting -- via the Report Command Set --
// that the caller already has fromRev's own content, at one uniform
// revision (the same shape [IsSingleRevisionUpdate] recognizes on the
// server side: a single, non-start-empty, root "set-path"), then parses
// the resulting Editor Command Set sequence by calling editor's own
// fields for only what actually changed on the way to wantRev (nil
// meaning the latest): an unmodified file or directory is never
// mentioned at all, a modified file's new content arrives via
// Editor.OpenFile/ApplyTextdelta/CloseFile, and added/removed nodes via
// Editor.AddDir/AddFile/DeleteEntry. It returns the revision actually
// reached (whatever the server's own "target-rev" reported).
//
// Update itself never touches a filesystem, a database, or any other
// storage, the same way [Client.Checkout] doesn't: it is the caller's own
// editor (see [Editor]'s own doc comment) that must already reflect
// fromRev's own content -- normally wherever an earlier Checkout (or
// Update) call of the same fromRev left it -- and that turns each
// callback into whatever "updating" means for it (e.g. Editor.OpenFile
// reading a real file's current on-disk content, Editor.CloseFile
// overwriting it). See cmd/go-svn's own "update" subcommand for a
// filesystem-backed implementation built purely on this API. Update has
// no way to verify on its own that editor's own backing state actually
// matches fromRev: a file whose current content no longer matches what
// the server believes fromRev's own content to be is caught (see
// Editor.OpenFile's own doc comment on the checksum this verifies), but
// a structure that has otherwise drifted (an extra local file, say) is
// not detected at all.
//
// Only a single-revision (not "mixed-revision") update is supported, the
// same limitation [Server.UpdateEdit] has on the serving side: Update
// always reports everything as being at one uniform revision, with no
// way to describe part of it as pinned to an older one.
//
// Unlike every other Client method, Update does not retry on a broken
// connection, for the same reason [Client.Checkout] doesn't: reconnecting
// and resending "update" from scratch would call editor's own callbacks
// again for whatever they already ran for, and a fresh attempt still
// assumes editor's own backing state reflects nothing newer than
// fromRev, which may no longer hold once some of its callbacks have
// already fired.
func (c *Client) Update(fromRev int, wantRev *int, editor Editor) (checkedOutRev int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	rev, err := c.reportAndApply(wantRev, fromRev, false, editor)
	if err != nil {
		return 0, fmt.Errorf("svn: update: %w", err)
	}
	return rev, nil
}
