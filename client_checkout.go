package svn

import "fmt"

// Checkout sends an "update" command for whatever Connect/NewClient was
// given (exactly like Stat/List/GetFile's own "" path argument -- there
// is no separate path argument here at all, for the reason given below)
// at rev (nil meaning the latest revision), reporting -- via the Report
// Command Set -- that the caller has nothing at all yet (the same
// "start-empty" shape [IsPlainCheckout] recognizes on the server side),
// then parses the resulting Editor Command Set sequence by calling
// editor's own fields for every node it describes, exactly as "svn
// checkout" would. It returns the revision actually checked out
// (whatever the server's own "target-rev" reported).
//
// Checkout itself never touches a filesystem, a database, or any other
// storage: what "checking out" actually produces is entirely up to
// editor (see [Editor]'s own doc comment) -- a real, on-disk working
// copy, an in-memory tree, or anything else a caller wants. See
// cmd/go-svn's own "checkout" subcommand for a filesystem-backed
// implementation built purely on this API.
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
// working copy in place (keeping its own "trunk/..." nesting), but the
// wrong one for a fresh, standalone checkout of just that subdirectory
// -- confirmed the hard way: an earlier version of this method took a
// path parameter and sent it as target, which described "trunk" itself
// as a child of the root instead of becoming the root.
//
// Unlike every other Client method, Checkout does not retry on a broken
// connection: reconnecting and resending "update" from scratch would
// call editor's own callbacks again for whatever they already ran for,
// which a caller building up state incrementally (e.g. writing files as
// they arrive) generally can't safely redo from scratch either.
func (c *Client) Checkout(wantRev *int, editor Editor) (checkedOutRev int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Resolved once, up front, purely for "set-path"'s own rev field
	// reportAndApply sends -- see its own doc comment for why this is
	// needed at all.
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

	rev, err := c.reportAndApply(wantRev, reportRev, true, editor)
	if err != nil {
		return 0, fmt.Errorf("svn: checkout: %w", err)
	}
	return rev, nil
}
