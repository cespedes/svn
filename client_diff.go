package svn

import "fmt"

// Diff sends a "diff" command for whatever Connect/NewClient was given
// (see [Client.Checkout]'s own doc comment for why there is no separate
// path argument), reporting -- via the Report Command Set -- that the
// caller already has fromRev's own content, at one uniform revision (the
// same shape [IsSingleRevisionUpdate] recognizes on the server side,
// exactly like [Client.Update]'s own report), then parses the resulting
// Editor Command Set sequence by calling editor's own fields for only
// what actually changed between fromRev and wantRev (nil meaning the
// latest): an unmodified file or directory is never mentioned at all, a
// modified file's old and new content are both available to the caller
// (Editor.OpenFile's own return value is the "before" side, and
// Editor.CloseFile's own content argument is the "after" side), and
// added/removed nodes via Editor.AddDir/AddFile/DeleteEntry. It returns
// the revision actually reached (whatever the server's own "target-rev"
// reported).
//
// Diff reuses the exact same report/editor exchange [Client.Update]
// does, and so the exact same [Editor] -- there is no dedicated
// "diff-only" callback shape, since the underlying wire exchange a real
// svnserve drives is identical either way (see [Server.Diff]'s own doc
// comment on the server side). What differs is only what the caller does
// with the result: Update overwrites its own backing state with the new
// content, while Diff would typically compute and print a textual diff
// from the old/new content pair instead, without changing anything.
// Actually producing that textual diff (e.g. a unified diff) is up to
// the caller -- like everywhere else in this package, Diff itself never
// touches a filesystem or does any text processing of its own; see
// cmd/go-svn's own "diff" subcommand for one built purely on this API.
//
// Diff is deliberately as constrained as [Server.Diff] already is on the
// serving side: it always compares fromRev/wantRev of the exact same
// repository location this Client is connected to (sent as the "diff"
// command's own versusURL, the same URL Connect/NewClient greeted the
// server with) -- the only case this package implements end to end.
// Comparing two different repository locations needs the
// target-selection logic a real "svn diff OLD-URL NEW-URL" uses, which
// this method does not provide.
//
// Whether editor.OpenFile's own "before" content comes from a real,
// already-checked-out working copy (as [Client.Update]'s own would) or
// is fetched some other way (e.g. a second Client connected to the same
// repository, since this one is busy driving the exchange for the
// duration of the call -- see below) is entirely up to editor: Diff
// itself has no notion of a working copy, so a bare-repository diff
// (nothing checked out locally at all) works exactly the same way, as
// long as editor.OpenFile can produce fromRev's own content for a path
// somehow.
//
// Only a single-revision (not "mixed-revision") comparison is supported,
// the same limitation [Client.Update]/[Server.UpdateEdit] have.
//
// Unlike every other Client method, Diff does not retry on a broken
// connection, for the same reason [Client.Checkout]/[Client.Update]
// don't: reconnecting and resending "diff" from scratch would call
// editor's own callbacks again for whatever they already ran for.
func (c *Client) Diff(fromRev int, wantRev *int, editor Editor) (rev int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lrev := []int{}
	if wantRev != nil {
		lrev = append(lrev, *wantRev)
	}
	// params: ( [rev] target:string recurse:bool ignore-ancestry:bool
	//   url:string ? text-deltas:bool ? depth:word ), confirmed against
	//   a real svnserve (see server.go's own "diff" case) -- target is
	//   always "" for the same reason Checkout/Update's own target
	//   always is (see Checkout's own doc comment), ignore-ancestry is
	//   left false (this package doesn't model copy ancestry at all),
	//   text-deltas is true (Diff is pointless without the actual
	//   content), and depth is "infinity" to match Checkout/Update's own
	//   report.
	rev, err = c.reportAndApply("diff",
		[]any{lrev, []byte(""), true, false, []byte(c.address), true, "infinity"},
		fromRev, false, editor)
	if err != nil {
		return 0, fmt.Errorf("svn: diff: %w", err)
	}
	return rev, nil
}
