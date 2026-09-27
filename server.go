package svn

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// ReportedPath is one entry from a client's report describing what it
// already has, driving an "update" (or, eventually, "switch"/"status"/
// "diff"): the accumulated result of one "set-path" command. A future
// "delete-path"/"link-path" command would add further entries here, once
// those are implemented -- see FinishReport.
type ReportedPath struct {
	// Path is the reported path, relative to the report's target (the
	// path Update was called with).
	Path string
	// Rev is the revision the client already has at Path.
	Rev uint
	// StartEmpty reports whether the client has nothing at all at Path
	// (e.g. because it is a brand new checkout), rather than a real,
	// possibly-stale copy at Rev.
	StartEmpty bool
}

// A Server defines parameters for running a SVN server.
//
// Each exported func field is a callback invoked when Serve receives the
// matching command; leaving a field nil makes Serve reply "unimplemented"
// to the client for that command, without calling anything.
type Server struct {
	// ReposInfo holds the repository information (UUID, root URL,
	// capabilities) sent to the client during the handshake. If Greet is
	// set, it is responsible for filling this in instead; see Greet.
	ReposInfo ReposInfo

	// Greet, if set, is called once per connection with the version,
	// capabilities and URL the client sent, plus its optional "ra-client"
	// and "client" version strings (client is nil if the client omitted
	// it). It must return the ReposInfo to report back to the client, or
	// an error to reject the connection. If Greet is nil, Serve reports
	// ReposInfo.URL as whatever URL the client sent, an empty
	// Capabilities list, and a hardcoded placeholder UUID.
	Greet func(version int, capabilities []string, url string, raclient string, client *string) (ReposInfo, error)

	// GetLatestRev answers a "get-latest-rev" command, returning the
	// repository's latest revision number.
	GetLatestRev func() (int, error)

	// Stat answers a "stat" command, returning the status of path at rev
	// (or at the latest revision, if rev is nil). If path does not exist,
	// Stat should return an error satisfying errors.Is(err, fs.ErrNotExist);
	// Serve reports that to the client as a real svnserve does (a
	// successful response whose single entry slot is an empty list),
	// rather than as a failure.
	Stat func(path string, rev *uint) (Dirent, error)

	// CheckPath answers a "check-path" command, returning the node kind
	// ("file", "dir" or "none") of path at rev (or at the latest revision,
	// if rev is nil).
	CheckPath func(path string, rev *uint) (string, error)

	// List answers a "list" command, returning the direct children of
	// path at rev, PLUS an entry for path itself. depth is one of the
	// protocol's depth words (e.g. "immediates"), fields selects which
	// optional Dirent fields the client wants populated, and pattern, if
	// non-empty, restricts the result to entries matching one of the
	// given glob patterns.
	//
	// Each entry's Dirent.Path must be the full, slash-prefixed path from
	// the repository root (e.g. "/trunk/main.go", not "main.go" or
	// "trunk/main.go") -- confirmed against a real svnserve's own
	// response, which always uses this form and always includes the
	// queried directory itself as one of the entries (Path equal to path,
	// prefixed the same way). Serve sends whatever Dirent.Path contains
	// as-is: an earlier version of this field instead had Serve rebuild
	// each path itself from a bare base name, which broke as soon as the
	// client's session was anchored below the repository root (e.g. it
	// connected directly to ".../repo/trunk"): path is then relative to
	// that anchor, not to the repository root, so the rebuilt paths came
	// out wrong -- and a real svn client has been seen to segfault on the
	// resulting malformed "list" response.
	List func(path string, rev *uint, depth string, fields []string, pattern []string) ([]Dirent, error)

	// GetFile answers a "get-file" command, returning the revision the
	// content came from, the file's properties (if wantProps), and its
	// content (if wantContents).
	GetFile func(path string, rev *uint, wantProps bool, wantContents bool) (uint, []PropList, []byte, error)

	// Log answers a "log" command, returning the log entries for paths
	// between startRev and endRev. changedPaths reports whether the
	// client asked for each LogEntry's Changed field to be populated.
	Log func(paths []string, startRev uint, endRev uint, changedPaths bool) ([]LogEntry, error)

	// Update is called for an "update" command, with the client's
	// requested target revision (nil meaning the latest), the target path
	// within the repository, and whether the update should recurse.
	// It has no return value because the protocol does not reply to
	// "update" beyond an initial acknowledgement: the actual result is
	// driven by the report commands that follow (set-path, ...,
	// finish-report), which Serve accumulates on the caller's behalf and
	// hands to FinishReport -- see FinishReport.
	Update func(rev *uint, target string, recurse bool)

	// Diff is called for a "diff" command, with the client's requested
	// comparison revision (nil meaning the latest), the target path
	// within the repository, whether to recurse, whether to ignore
	// ancestry (copy history) when matching paths, the URL to compare
	// against (a real client sends the same URL it connected to, for a
	// same-path, two-revision diff; anything else needs target-selection
	// logic this package does not implement), whether the client wants
	// full text deltas, and the requested depth. Like Update, it has no
	// return value: the actual result is driven by the same report/
	// editor exchange that follows "update" (set-path, ...,
	// finish-report) -- a Server.FinishReport that already handles
	// "update" via IsSingleRevisionUpdate/UpdateEdit needs no separate
	// logic for "diff": the accumulated report looks identical either
	// way, and the editor sequence UpdateEdit builds (open/add/delete/
	// modify against a second revision) is exactly what a real client
	// needs to compute a diff for itself, having kept the "from"
	// revision's content locally instead of overwriting it.
	Diff func(rev *uint, target string, recurse bool, ignoreAncestry bool, versusURL string, textDeltas bool, depth string)

	// Switch is called for a "switch" command, with the client's
	// requested target revision (nil meaning the latest), the target
	// path within the repository, whether to switch recursively, the URL
	// to switch to, the requested depth, whether the client wants
	// copy-from arguments, and whether to ignore ancestry. Like Update
	// and Diff, it has no return value: the actual result is driven by
	// the same report/editor exchange that follows (set-path, ...,
	// finish-report), except that a Server.FinishReport implementation
	// now needs a report/editor helper that diffs two different paths
	// (see [Server.SwitchEdit]) rather than the same path across two
	// revisions.
	//
	// A real client often reuses an existing session anchored elsewhere,
	// reparenting it as needed (see Reparent) before finally reparenting
	// back to the switch target's own current location and sending
	// "switch" with an empty target relative to that anchor -- so, like
	// Diff's versusURL, url is generally the only reliable source of the
	// real destination path; see [RepoRelativePath].
	Switch func(rev *uint, target string, recurse bool, url string, depth string, sendCopyfromArgs bool, ignoreAncestry bool)

	// Reparent is called for a "reparent" command, changing the
	// session's own anchor (the path every subsequent command's own path
	// argument is relative to) to url without opening a new connection.
	// It is purely informational, like SetPath: Serve itself has no
	// notion of "the session's anchor" (every path argument is passed
	// through to a callback exactly as the client sent it), so a Server
	// implementation whose callbacks resolve a path against a
	// remembered anchor (the same one Greet's own connectURL argument
	// seeded) needs Reparent to learn about it changing -- a real
	// client commonly reparents an existing session back and forth
	// while preparing a "switch" (see Switch), rather than opening a
	// second connection. Serve replies "unimplemented" if Reparent is
	// nil, which a real client's "switch" cannot proceed past.
	Reparent func(url string) error

	// SetPath is called for a "set-path" command, part of the report
	// mechanism a client uses to describe what it already has before an
	// update. It is purely informational: Serve records every set-path
	// call itself (see FinishReport) whether or not SetPath is set, so
	// leaving it nil does not lose any information -- set it only to
	// observe each call as it happens (e.g. logging).
	SetPath func(path string, rev uint, startEmpty bool)

	// FinishReport answers a "finish-report" command, which ends a report
	// describing what the client already has (accumulated, in the order
	// the client sent them, from every "set-path" command since the
	// preceding "update") and should drive an editor command sequence
	// (open-root, ..., close-edit) back to the client. It must return the
	// sequence of editor commands as Items, or an error to send an
	// "abort-edit" instead.
	FinishReport func(paths []ReportedPath) ([]Item, error)

	// Debug, if non-nil, makes Serve log every Item it reads from or
	// writes to the connection here, prefixed with "> " (sent) or "< "
	// (received) -- the same convention e.g. "curl -v" uses. The initial
	// greeting is included, unlike [Client.SetDebug]'s own handshake
	// exclusion, since Serve writes it itself rather than receiving an
	// already-handshaken connection.
	Debug io.Writer
}

// errMalformedNetworkData is the failure Serve reports when it can't
// unmarshal a command's own params -- confirmed against a real svnserve
// to use this exact code/message for that case.
var errMalformedNetworkData = Error{
	AprErr:  210004,
	Message: "Malformed network data",
}

// Serve sends and receives SVN messages against a client,
// issuing calls to the respective functions when a message
// is received.
//
// Serve always returns a non-nil error: [io.EOF] once the connection ends
// cleanly, or another error otherwise.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	conn := conn{
		r:     r,
		w:     w,
		debug: s.Debug,
	}

	var err error
	var item Item
	// report accumulates the current update/switch report's entries,
	// between an "update" and its "finish-report"; see ReportedPath.
	var report []ReportedPath

	err = conn.WriteSuccess([]any{
		SvnVersion,
		SvnVersion,
		[]any{},
		[]any{
			"edit-pipeline",
			"svndiff1",
			"accepts-svndiff2",
			"absent-entries",
			"commit-revprops",
			"depth",
			"log-revprops",
			"atomic-revprops",
			"partial-replay",
			"inherited-props",
			"ephemeral-txnprops",
			"file-revs-reverse",
			"list",
		},
	})
	if err != nil {
		return err
	}

	var greet struct {
		Version      int
		Capabilities []string
		URL          string
		RAClient     string
		Client       []string
	}

	err = conn.Read(&greet)
	if err != nil {
		return err
	}
	if s.Greet != nil {
		var pclient *string
		if len(greet.Client) > 0 {
			pclient = &greet.Client[0]
		}
		s.ReposInfo, err = s.Greet(greet.Version, greet.Capabilities, greet.URL, greet.RAClient, pclient)
		if err != nil {
			conn.WriteFailure(err)
			return err
		}
	} else {
		s.ReposInfo.UUID = "c5a7a7b1-3e3e-4c98-a541-f46ece210564"
		s.ReposInfo.URL = greet.URL
		s.ReposInfo.Capabilities = make([]string, 0)
	}

	// Sending "auth-request":
	err = conn.WriteSuccess([]any{
		[]any{
			"ANONYMOUS",
			"EXTERNAL",
		},
		[]byte(s.ReposInfo.UUID),
	})
	if err != nil {
		return err
	}

	// Reading "auth-response" from client
	err = conn.Read(&item)
	if err != nil {
		return err
	}

	// no matter what "auth-response" the client sent, we always reply success
	err = conn.WriteSuccess([]any{})
	if err != nil {
		return err
	}

	// and finally, we send a command response with UUID, URL and capabilities:
	err = conn.WriteSuccess([]any{
		[]byte(s.ReposInfo.UUID),
		[]byte(s.ReposInfo.URL),
		s.ReposInfo.Capabilities,
	})
	if err != nil {
		return err
	}

	for {
		var item Item
		var command struct {
			Name   string
			Params Item
		}
		err = conn.Read(&item)
		if err != nil {
			return err
		}
		err = Unmarshal(item, &command)
		if err != nil {
			return err
		}
		// log.Printf("server received command %q %v\n", command.Name, command.Params)
		switch command.Name {
		case "get-latest-rev":
			err = s.handleGetLatestRev(conn)
		case "stat":
			err = s.handleStat(conn, command.Params)
		case "list":
			err = s.handleList(conn, command.Params)
		case "get-dir":
			err = s.handleGetDir(conn, command.Params)
		case "check-path":
			err = s.handleCheckPath(conn, command.Params)
		case "get-iprops":
			err = s.handleGetIProps(conn, command.Params)
		case "get-file":
			err = s.handleGetFile(conn, command.Params)
		case "log":
			err = s.handleLog(conn, command.Params)
		case "update":
			err = s.handleUpdate(conn, command.Params)
		case "diff":
			err = s.handleDiff(conn, command.Params)
		case "switch":
			err = s.handleSwitch(conn, command.Params)
		case "reparent":
			err = s.handleReparent(conn, command.Params)
		case "set-path": // From the Report Command Set
			err = s.handleSetPath(conn, command.Params, &report)
		case "finish-report": // From the Report Command Set
			err = s.handleFinishReport(conn, &report)
		default:
			err = conn.WriteFailure(Error{
				AprErr:  210001,
				Message: fmt.Sprintf("Unknown command '%s'", command.Name),
			})
			// ( failure ( ( 210001 34:Unknown editor command 'no-existe' 0: 0 ) ) )
		}
		if err != nil {
			return err
		}
	}
}

// handleGetLatestRev answers a "get-latest-rev" command.
func (s *Server) handleGetLatestRev(conn conn) error {
	if s.GetLatestRev == nil {
		return replyUnimplemented(conn, "get-latest-rev")
	}
	rev, err := s.GetLatestRev()
	if err != nil {
		return conn.WriteFailure(err)
	}
	// empty auth-request:
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{rev})
}

// handleStat answers a "stat" command.
// params: ( path:string [ rev:number ] )
func (s *Server) handleStat(conn conn, params Item) error {
	if s.Stat == nil {
		return replyUnimplemented(conn, "stat")
	}
	var args struct {
		Path string
		Rev  *uint
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	entry, err := s.Stat(args.Path, args.Rev)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A real svnserve reports a nonexistent path as a
			// successful response, not a failure -- Stat callbacks
			// signal this the same way [Client.Stat] itself does,
			// by returning an error satisfying
			// errors.Is(err, fs.ErrNotExist).
			if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			// response: ( ? entry:dirent ). The tuple always has
			// exactly one slot; an absent optional is that slot
			// holding an empty list ( ( ) ), not the tuple itself
			// having zero elements ( ) -- confirmed against a real
			// svn client, which rejects the latter as malformed
			// (see TestServerAgainstRealSVNClient's "info on
			// nonexistent path" subtest).
			return conn.WriteSuccess([]any{[]any{}})
		}
		return conn.WriteFailure(err)
	}
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	// response: ( ? entry:dirent ). A "?"/optional marker always
	// wraps whatever it marks in its own 0-or-1-element list; since
	// "entry" here is itself a compound dirent tuple (which is
	// naturally its own list), a present entry ends up nested two
	// levels deep. Confirmed against a real svnserve's own wire
	// response, which sends exactly this shape.
	return conn.WriteSuccess([]any{[]any{[]any{
		entry.Kind,
		entry.Size,
		entry.HasProps,
		entry.CreatedRev,
		[]any{[]byte(entry.CreatedDate)},
		[]any{[]byte(entry.LastAuthor)},
	}}})
}

// handleList answers a "list" command.
// params: ( path:string [ rev:number ] depth:word ( field:dirent-field ... ) ? ( pattern:string ... ) )
func (s *Server) handleList(conn conn, params Item) error {
	if s.List == nil {
		return replyUnimplemented(conn, "list")
	}
	var args struct {
		Path    string
		Rev     *uint
		Depth   string
		Fields  []string
		Pattern []string
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	dirents, err := s.List(args.Path, args.Rev, args.Depth, args.Fields, args.Pattern)
	if err != nil {
		return conn.WriteFailure(err)
	}
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	for _, d := range dirents {
		if err := conn.Write([]any{
			[]byte(d.Path),
			d.Kind,
			[]any{d.Size},
			[]any{d.HasProps},
			[]any{d.CreatedRev},
			[]any{[]byte(d.CreatedDate)},
			[]any{[]byte(d.LastAuthor)},
		}); err != nil {
			return err
		}
	}
	if err := conn.Write("done"); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{})
}

// handleGetDir answers a "get-dir" command.
// params: ( path:string [ rev:number ] want-props:bool
//
//	want-contents:bool ? ( field:dirent-field ... )
//	? want-iprops:bool )
//
// response: ( rev:number props:proplist ( entry:dirent ... )
//
//	[ inherited-props:iproplist ] )
//
// dirent: ( name:string kind:word size:number has-props:bool
//
//	created-rev:number [ created-date:string ]
//	[ last-author:string ] )
//
// The older, pre-"list" way to read a directory's children. A modern
// client generally uses "list" instead (see Server.List's own doc
// comment), but "svn diff" still falls back to this to enumerate a
// deleted directory's former contents, so it can describe every file
// that disappeared along with it -- confirmed by a real client sending
// this in exactly that situation, despite this package always
// advertising the "list" capability. Answered entirely in terms of
// Server.List: a directory's own properties are always reported empty
// (the same simplification "get-iprops" already makes), and, unlike
// "list", every dirent field is always populated, regardless of which
// ones the client actually asked for.
func (s *Server) handleGetDir(conn conn, params Item) error {
	if s.List == nil {
		return replyUnimplemented(conn, "get-dir")
	}
	var args struct {
		Path         string
		Rev          *uint
		WantProps    bool
		WantContents bool
		Fields       []string
		WantIProps   bool
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	entries, err := s.List(args.Path, args.Rev, "immediates",
		[]string{"kind", "size", "created-rev", "time", "last-author"}, nil)
	if err != nil {
		return conn.WriteFailure(err)
	}
	self, children := splitCheckoutEntries(entries)
	var dirRev uint
	var selfPath string
	if self != nil {
		dirRev = self.CreatedRev
		selfPath = self.Path
	}
	dirents := []any{}
	if args.WantContents {
		for _, c := range children {
			dirents = append(dirents, []any{
				[]byte(childName(c, selfPath)),
				c.Kind,
				c.Size,
				c.HasProps,
				c.CreatedRev,
				[]any{[]byte(c.CreatedDate)},
				[]any{[]byte(c.LastAuthor)},
			})
		}
	}
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{dirRev, []any{}, dirents})
}

// handleCheckPath answers a "check-path" command.
// params: ( path:string [ rev:number ] )
func (s *Server) handleCheckPath(conn conn, params Item) error {
	if s.CheckPath == nil {
		return replyUnimplemented(conn, "check-path")
	}
	var args struct {
		Path string
		Rev  *uint
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	kind, err := s.CheckPath(args.Path, args.Rev)
	if err != nil {
		return conn.WriteFailure(err)
	}
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{kind})
}

// handleGetIProps answers a "get-iprops" command.
// params: ( path:string [ rev:number ] )
// response: ( inherited-props:iproplist )
//
// Inherited properties (a path's ancestor directories' properties, e.g.
// svn:auto-props set higher up the tree) aren't modeled anywhere in this
// package -- List/GetFile have no notion of a directory's own properties
// at all -- so this always reports none. Unlike every other command
// here, it isn't gated behind a nil-able callback field: a real client
// needs some answer to it to complete even a plain checkout of a path
// below the repository root (confirmed: it otherwise fails outright with
// "E210001: Unknown command 'get-iprops'", which is exactly how this
// case was found).
func (s *Server) handleGetIProps(conn conn, params Item) error {
	var args struct {
		Path string
		Rev  *uint
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	// empty auth-request:
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{[]any{}})
}

// handleGetFile answers a "get-file" command.
// params: ( path:string [ rev:number ] want-props:bool want-contents:bool ? want-iprops:bool )
func (s *Server) handleGetFile(conn conn, params Item) error {
	if s.GetFile == nil {
		return replyUnimplemented(conn, "get-file")
	}
	var args struct {
		Path         string
		Rev          *uint
		WantProps    bool
		WantContents bool
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	rev, proplist, contents, err := s.GetFile(args.Path, args.Rev, args.WantProps, args.WantContents)
	if err != nil {
		return conn.WriteFailure(err)
	}
	checksum := []byte(fmt.Sprintf("%x", md5.Sum(contents)))
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	if err := conn.WriteSuccess([]any{[]any{checksum}, rev, proplist}); err != nil {
		return err
	}
	if args.WantContents {
		if err := conn.Write(contents); err != nil {
			return err
		}
		if err := conn.Write([]byte{}); err != nil {
			return err
		}
		return conn.WriteSuccess([]any{})
	}
	return nil
}

// handleLog answers a "log" command.
// params: ( ( target-path:string ... ) [ start-rev:number ] [ end-rev:number ] changed-paths:bool strict-node:bool ? limit:number ? include-merged-revisions:bool all-revprops | revprops ( revprop:string ... ) )
func (s *Server) handleLog(conn conn, params Item) error {
	if s.Log == nil {
		return replyUnimplemented(conn, "log")
	}
	var args struct {
		Paths                  []string
		StartRev               uint
		EndRev                 uint
		ChangedPaths           bool
		StrictNode             bool
		Limit                  int
		IncludeMergedRevisions bool
		RevpropsType           string
		Revprops               []string
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	logEntries, err := s.Log(args.Paths, args.StartRev, args.EndRev, args.ChangedPaths)
	if err != nil {
		return conn.WriteFailure(err)
	}
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	for _, l := range logEntries {
		// changed-path-entry: ( path:string mode:word
		//   ? ( copy-path:string copy-rev:number )
		//   ? ( node-kind:string text-mods:bool prop-mods:bool ) )
		// Built by hand, rather than left to Marshal, because
		// Path/CopyPath are plain Go strings for callers'
		// convenience: Marshal would encode those as words,
		// which breaks (confirmed against a real svnserve) as
		// soon as a path contains a character a bare word can't
		// hold, like '/'.
		var changed []any
		for _, cp := range l.Changed {
			var copyGroup any = []any{}
			if cp.Copy != nil {
				copyGroup = []any{[]byte(cp.Copy.Path), cp.Copy.Rev}
			}
			var infoGroup any = []any{}
			if cp.Info != nil {
				infoGroup = []any{[]byte(cp.Info.NodeKind), cp.Info.TextMods, cp.Info.PropMods}
			}
			changed = append(changed, []any{
				[]byte(cp.Path),
				cp.Mode,
				copyGroup,
				infoGroup,
			})
		}
		if err := conn.Write([]any{
			changed,
			l.Rev,
			[]any{[]byte(l.Author)},
			[]any{[]byte(l.Date)},
			[]any{[]byte(l.Message)},
		}); err != nil {
			return err
		}
	}
	if err := conn.Write("done"); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{})
}

// handleUpdate answers an "update" command.
func (s *Server) handleUpdate(conn conn, params Item) error {
	if s.Update == nil {
		return replyUnimplemented(conn, "update")
	}
	var args struct {
		Rev     *uint
		Target  string
		Recurse bool
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	s.Update(args.Rev, args.Target, args.Recurse)
	// empty auth-request:
	return conn.WriteSuccess([]any{[]any{}, []byte{}})
}

// handleDiff answers a "diff" command.
// params: ( [ rev:number ] target:string recurse:bool
//
//	ignore-ancestry:bool url:string ? text-deltas:bool
//	? depth:word )
func (s *Server) handleDiff(conn conn, params Item) error {
	if s.Diff == nil {
		return replyUnimplemented(conn, "diff")
	}
	var args struct {
		Rev            *uint
		Target         string
		Recurse        bool
		IgnoreAncestry bool
		VersusURL      string
		TextDeltas     bool
		Depth          string
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	s.Diff(args.Rev, args.Target, args.Recurse, args.IgnoreAncestry, args.VersusURL, args.TextDeltas, args.Depth)
	// empty auth-request: acked immediately, exactly like "update" --
	// confirmed by raw wire capture, reading right after sending "diff"
	// and before sending "set-path" (a first attempt assumed this ack
	// was deferred until "finish-report", from a capture that sent
	// set-path and finish-report before reading anything at all, which
	// couldn't actually tell the two apart; a real client left waiting
	// for this ack immediately, since it never comes with that
	// assumption, is what caught the mistake).
	return conn.WriteSuccess([]any{[]any{}, []byte{}})
}

// handleSwitch answers a "switch" command.
// params: ( [ rev:number ] target:string recurse:bool
//
//	url:string ? depth:word send-copyfrom-args:bool
//	ignore-ancestry:bool )
//
// Confirmed by raw wire capture against a real "svn switch":
// depth/send-copyfrom-args/ignore-ancestry come right after url, not
// interleaved with recurse/ignore-ancestry the way "diff"'s own params
// are ordered.
func (s *Server) handleSwitch(conn conn, params Item) error {
	if s.Switch == nil {
		return replyUnimplemented(conn, "switch")
	}
	var args struct {
		Rev              *uint
		Target           string
		Recurse          bool
		URL              string
		Depth            string
		SendCopyfromArgs bool
		IgnoreAncestry   bool
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	s.Switch(args.Rev, args.Target, args.Recurse, args.URL, args.Depth, args.SendCopyfromArgs, args.IgnoreAncestry)
	// empty auth-request: acked immediately, same as "update"/"diff".
	return conn.WriteSuccess([]any{[]any{}, []byte{}})
}

// handleReparent answers a "reparent" command.
// params: ( url:string )
// response: ( )
//
// Unlike "update"/"diff"/"switch", this gets a real second response
// beyond the empty auth-request pre-ack (confirmed by raw wire capture):
// a real client waits for both before sending its next command.
func (s *Server) handleReparent(conn conn, params Item) error {
	if s.Reparent == nil {
		return replyUnimplemented(conn, "reparent")
	}
	var args struct {
		URL string
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	if err := s.Reparent(args.URL); err != nil {
		return conn.WriteFailure(err)
	}
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{})
}

// handleSetPath answers a "set-path" command, from the Report Command
// Set: accumulates the reported path into *report regardless of whether
// Server.SetPath is set (see its own doc comment); there is no response.
func (s *Server) handleSetPath(conn conn, params Item, report *[]ReportedPath) error {
	var args struct {
		Path       string
		Rev        uint
		StartEmpty bool
	}
	if err := Unmarshal(params, &args); err != nil {
		return conn.WriteFailure(errMalformedNetworkData)
	}
	*report = append(*report, ReportedPath{
		Path: args.Path, Rev: args.Rev, StartEmpty: args.StartEmpty,
	})
	if s.SetPath != nil {
		s.SetPath(args.Path, args.Rev, args.StartEmpty)
	}
	return nil
}

// handleFinishReport answers a "finish-report" command, from the Report
// Command Set, ending the exchange *report accumulated: on success,
// drives the returned []Item back to the client followed by close-edit;
// on error, sends abort-edit instead, reading the client's own ack
// either way before answering "finish-report" itself (a real client acks
// abort-edit the same way it acks close-edit -- reported as a real bug
// found by TestServerFinishReportErrorReadsAbortEditAck: leaving that
// ack unread desyncs the connection, so the next thing the client sends
// gets misread as a bogus command). *report is reset to nil once
// handled, whether or not Server.FinishReport is even set.
func (s *Server) handleFinishReport(conn conn, report *[]ReportedPath) error {
	if s.FinishReport == nil {
		*report = nil
		return replyUnimplemented(conn, "finish-report")
	}
	// no response?
	if err := conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
		return err
	}
	items, err := s.FinishReport(*report)
	*report = nil
	if err != nil {
		if err := conn.Write([]any{"abort-edit", []any{}}); err != nil {
			return err
		}
		var ack Item
		if err := conn.ReadResponse(&ack); err != nil {
			return err
		}
		return conn.WriteSuccess([]any{})
	}
	for _, i := range items {
		if err := conn.Write(i); err != nil {
			return err
		}
	}
	if err := conn.Write([]any{"close-edit", []any{}}); err != nil {
		return err
	}
	var ack Item
	if err := conn.ReadResponse(&ack); err != nil {
		return err
	}
	return conn.WriteSuccess([]any{})
}

func replyUnimplemented(conn conn, cmd string) error {
	return conn.WriteFailure(Error{
		AprErr:  210001,
		Message: fmt.Sprintf("Command '%s' unimplemented", cmd),
	})
}
