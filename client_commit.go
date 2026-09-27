package svn

import "fmt"

// Commit sends a "commit" command with logMessage as the commit's log
// message, then streams items -- an Editor Command Set sequence
// describing the tree of changes being committed, built with
// [NewEditorWriter] the exact same way [Server.FinishReport] already
// builds one for the read direction, just travelling in the opposite
// direction on the wire -- and returns the resulting [CommitInfo].
//
// Unlike Checkout/Update/Diff, Commit is the one driving an Editor
// Command Set, not parsing one: there is no [Editor] callback involved,
// since the caller already has the whole sequence in hand, as items,
// before calling Commit at all.
//
// Confirmed against a real svnserve: after "commit", the server acks it
// (the usual empty-auth-request pre-ack, followed by a plain empty
// success meaning "go ahead and stream the edit"); items is then written
// with no acks read in between, matching a real client's own behavior;
// "close-edit" is sent and its ack read next (mirroring how [Server]'s
// own finish-report handling reads a client's close-edit ack in the
// other direction); and only then does the server send the deferred,
// real response to the original "commit" command: another
// empty-auth-request pre-ack, followed by the commit-info tuple itself
// -- sent bare, unlike every other command's response, without a
// "( success ( ... ) )" envelope around it (confirmed against a real
// svnserve; a "( failure ( ... ) )" envelope is still used for an error
// at this stage, e.g. an out-of-date commit, so Commit still recognizes
// and returns that shape as a Go error rather than an unmarshal-shape
// error).
//
// This package has no locking support, so Commit always sends an empty
// lock-tokens list and keep-locks=false. The log message is also
// resent as a "svn:log" revision property alongside it, matching what a
// real client does (a real svnserve reads it from there, not from the
// top-level log-message argument, on at least some code paths).
//
// Like Checkout/Update/Diff, Commit does not retry on a broken
// connection: reconnecting and resending "commit" from scratch could
// duplicate or conflict with a commit the server had already accepted.
func (c *Client) Commit(logMessage string, items []Item) (CommitInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// params: ( log-message:string ( lock-token:string ... ) keep-locks:bool
	//   ( ( name:string value:string ) ... ) ), confirmed against a real
	//   svnserve. The revprop name/value pair is built as raw []byte,
	//   not via [PropList] (whose fields are plain Go strings, which
	//   Marshal always encodes as a bare word -- fine for "svn:log"
	//   itself, but not for logMessage, which a real commit message
	//   routinely contains spaces or other characters a word can't
	//   hold; confirmed the hard way, since a real svnserve reports
	//   "Malformed network data" and hangs up entirely, rather than
	//   returning an ordinary protocol failure, given a wire-broken
	//   commit message this way).
	err := c.conn.Write([]any{
		"commit",
		[]any{
			[]byte(logMessage),
			[]any{},
			false,
			[]any{
				[]any{[]byte("svn:log"), []byte(logMessage)},
			},
		},
	})
	if err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: sending \"commit\": %w", err)
	}
	if err := c.handleAuth(); err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: %w", err)
	}
	var ready Item
	if err := c.conn.ReadResponse(&ready); err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: reading commit ack: %w", err)
	}

	for _, item := range items {
		if err := c.conn.Write(item); err != nil {
			return CommitInfo{}, fmt.Errorf("svn: commit: sending edit: %w", err)
		}
	}
	if err := c.conn.Write([]any{"close-edit", []any{}}); err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: sending close-edit: %w", err)
	}
	var closeAck Item
	if err := c.conn.ReadResponse(&closeAck); err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: reading close-edit ack: %w", err)
	}

	if err := c.handleAuth(); err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: %w", err)
	}
	var raw Item
	if err := c.conn.Read(&raw); err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: reading commit-info: %w", err)
	}
	if len(raw.List) > 0 && raw.List[0].Type == WordType && raw.List[0].Text == "failure" {
		if _, err := ParseResponse(raw); err != nil {
			return CommitInfo{}, err
		}
	}
	var info CommitInfo
	if err := Unmarshal(raw, &info); err != nil {
		return CommitInfo{}, fmt.Errorf("svn: commit: unmarshaling commit-info: %w", err)
	}
	return info, nil
}
