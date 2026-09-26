package svn

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

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
	// successful response with no entry), rather than as a failure.
	Stat func(path string, rev *uint) (Dirent, error)

	// CheckPath answers a "check-path" command, returning the node kind
	// ("file", "dir" or "none") of path at rev (or at the latest revision,
	// if rev is nil).
	CheckPath func(path string, rev *uint) (string, error)

	// List answers a "list" command, returning the direct children of
	// path at rev. depth is one of the protocol's depth words (e.g.
	// "immediates"), fields selects which optional Dirent fields the
	// client wants populated, and pattern, if non-empty, restricts the
	// result to entries matching one of the given glob patterns.
	//
	// Only each entry's own base name matters in its Dirent.Path (e.g.
	// "main.go", not "trunk/main.go" or any server-specific prefix):
	// Serve rebuilds the full wire path itself from path and that base
	// name. A real svn client has been seen to segfault given a
	// differently-shaped path here, so this is deliberately not left to
	// each List implementation to get right.
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
	// meant to be driven by the report commands that follow (SetPath,
	// ..., FinishReport), which is not implemented yet -- so Update
	// currently has no way to affect what, if anything, gets sent back.
	Update func(rev *uint, target string, recurse bool)

	// SetPath is called for a "set-path" command, part of the report
	// mechanism a client uses to describe what it already has before an
	// update. As with Update, there is no way to report a result back
	// from here yet: driving the resulting editor sequence is done in
	// FinishReport.
	SetPath func(path string, rev uint, startEmpty bool)

	// FinishReport answers a "finish-report" command, which ends a report
	// and should drive an editor command sequence (open-root, ...,
	// close-edit) back to the client. It must return the sequence of
	// editor commands as Items, or an error to send an "abort-edit"
	// instead.
	FinishReport func() ([]Item, error)
}

// Serve sends and receives SVN messages against a client,
// issuing calls to the respective functions when a message
// is received.
//
// Serve always returns a non-nil error: [io.EOF] once the connection ends
// cleanly, or another error otherwise.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	conn := conn{
		r: r,
		w: w,
	}

	var err error
	var item Item

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
		neterr := Error{
			AprErr:  210004,
			Message: "Malformed network data",
		}
		// log.Printf("server received command %q %v\n", command.Name, command.Params)
		switch command.Name {
		case "get-latest-rev":
			if s.GetLatestRev == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			rev, err := s.GetLatestRev()
			if err != nil {
				if err = conn.WriteFailure(err); err != nil {
					return err
				}
				continue
			}
			// empty auth-request:
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			if err = conn.WriteSuccess([]any{rev}); err != nil {
				return err
			}
		case "stat":
			// params: ( path:string [ rev:number ] )
			if s.Stat == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			var args struct {
				Path string
				Rev  *uint
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				if err = conn.WriteFailure(neterr); err != nil {
					return err
				}
				continue
			}
			entry, err := s.Stat(args.Path, args.Rev)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					// A real svnserve reports a nonexistent path as a
					// successful response with an empty (? entry:dirent),
					// not a failure -- see the WriteSuccess below. Stat
					// callbacks signal this the same way [Client.Stat]
					// itself does: by returning an error satisfying
					// errors.Is(err, fs.ErrNotExist).
					if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
						return err
					}
					if err = conn.WriteSuccess([]any{}); err != nil {
						return err
					}
					continue
				}
				if err = conn.WriteFailure(err); err != nil {
					return err
				}
				continue
			}
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			// response: ( ? entry:dirent ). A "?"/optional marker always
			// wraps whatever it marks in its own 0-or-1-element list; since
			// "entry" here is itself a compound dirent tuple (which is
			// naturally its own list), a present entry ends up nested two
			// levels deep. Confirmed against a real svnserve's own wire
			// response, which sends exactly this shape.
			if err = conn.WriteSuccess([]any{[]any{[]any{
				entry.Kind,
				entry.Size,
				entry.HasProps,
				entry.CreatedRev,
				[]any{[]byte(entry.CreatedDate)},
				[]any{[]byte(entry.LastAuthor)},
			}}}); err != nil {
				return err
			}
		case "list":
			// params: ( path:string [ rev:number ] depth:word ( field:dirent-field ... ) ? ( pattern:string ... ) )
			if s.List == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			var args struct {
				Path    string
				Rev     *uint
				Depth   string
				Fields  []string
				Pattern []string
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				if err = conn.WriteFailure(neterr); err != nil {
					return err
				}
				continue
			}
			dirents, err := s.List(args.Path, args.Rev, args.Depth, args.Fields, args.Pattern)
			if err != nil {
				if err = conn.WriteFailure(err); err != nil {
					return err
				}
				continue
			}
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			// The wire path for each entry is rebuilt here as "/" plus
			// the queried directory plus the entry's own base name,
			// rather than sent as whatever d.Path happens to contain:
			// a real svnserve always sends that full, slash-prefixed
			// form (confirmed against one), and at least one real svn
			// client has been seen to segfault on a "list" response
			// that instead uses a bare, unprefixed child name.
			listPrefix := "/" + strings.TrimPrefix(args.Path, "/")
			if listPrefix != "/" {
				listPrefix += "/"
			}
			for _, d := range dirents {
				if err = conn.Write([]any{
					[]byte(listPrefix + path.Base(d.Path)),
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
			if err = conn.Write("done"); err != nil {
				return err
			}
			if err = conn.WriteSuccess([]any{}); err != nil {
				return err
			}
		case "check-path":
			// params: ( path:string [ rev:number ] )
			if s.CheckPath == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			var args struct {
				Path string
				Rev  *uint
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				if err = conn.WriteFailure(neterr); err != nil {
					return err
				}
				continue
			}
			kind, err := s.CheckPath(args.Path, args.Rev)
			if err != nil {
				if err = conn.WriteFailure(err); err != nil {
					return err
				}
				continue
			}
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			if err = conn.WriteSuccess([]any{kind}); err != nil {
				return err
			}
		case "get-file":
			// params: ( path:string [ rev:number ] want-props:bool want-contents:bool ? want-iprops:bool )
			if s.GetFile == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			var args struct {
				Path         string
				Rev          *uint
				WantProps    bool
				WantContents bool
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				if err = conn.WriteFailure(neterr); err != nil {
					return err
				}
				continue
			}
			rev, proplist, contents, err := s.GetFile(args.Path, args.Rev, args.WantProps, args.WantContents)
			if err != nil {
				if err = conn.WriteFailure(err); err != nil {
					return err
				}
				continue
			}
			checksum := []byte(fmt.Sprintf("%x", md5.Sum(contents)))
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			if err = conn.WriteSuccess([]any{[]any{checksum}, rev, proplist}); err != nil {
				return err
			}
			if args.WantContents {
				if err = conn.Write(contents); err != nil {
					return err
				}
				if err = conn.Write([]byte{}); err != nil {
					return err
				}
				if err = conn.WriteSuccess([]any{}); err != nil {
					return err
				}
			}
		case "log":
			// params: ( ( target-path:string ... ) [ start-rev:number ] [ end-rev:number ] changed-paths:bool strict-node:bool ? limit:number ? include-merged-revisions:bool all-revprops | revprops ( revprop:string ... ) )
			if s.Log == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
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
			if err = Unmarshal(command.Params, &args); err != nil {
				if err = conn.WriteFailure(neterr); err != nil {
					return err
				}
				continue
			}
			logEntries, err := s.Log(args.Paths, args.StartRev, args.EndRev, args.ChangedPaths)
			if err != nil {
				if err = conn.WriteFailure(err); err != nil {
					return err
				}
				continue
			}
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			for _, l := range logEntries {
				// ( ( ) 7573 ( 3:noc ) ( 27:2024-04-02T13:37:34.350221Z ) ( 43:New open position: 2024-04-phd-visiting-apt ) false false 0 ( ) false )
				if err = conn.Write([]any{
					l.Changed,
					l.Rev,
					[]any{[]byte(l.Author)},
					[]any{[]byte(l.Date)},
					[]any{[]byte(l.Message)},
				}); err != nil {
					return err
				}
			}
			if err = conn.Write("done"); err != nil {
				return err
			}
			if err = conn.WriteSuccess([]any{}); err != nil {
				return err
			}
		case "update":
			if s.Update == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			var args struct {
				Rev     *uint
				Target  string
				Recurse bool
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				if err = conn.WriteFailure(neterr); err != nil {
					return err
				}
				continue
			}
			s.Update(args.Rev, args.Target, args.Recurse)
			// empty auth-request:
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
		case "set-path": // From the Report Command Set
			if s.SetPath == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			var args struct {
				Path       string
				Rev        uint
				StartEmpty bool
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				if err = conn.WriteFailure(neterr); err != nil {
					return err
				}
				continue
			}
			s.SetPath(args.Path, args.Rev, args.StartEmpty)
			// no response in set-path
		case "finish-report": // From the Report Command Set
			if s.FinishReport == nil {
				if err = replyUnimplemented(conn, command.Name); err != nil {
					return err
				}
				continue
			}
			// no response?
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			items, err := s.FinishReport()
			if err != nil {
				if err = conn.Write([]any{"abort-edit", []any{}}); err != nil {
					return err
				}
				continue
			}
			for _, i := range items {
				if err = conn.Write(i); err != nil {
					return err
				}
			}
			if err = conn.Write([]any{"close-edit", []any{}}); err != nil {
				return err
			}
			err = conn.ReadResponse(&item)
			if err != nil {
				return err
			}
			err = conn.WriteSuccess([]any{})
			if err != nil {
				return err
			}
		default:
			if err = conn.WriteFailure(Error{
				AprErr:  210001,
				Message: fmt.Sprintf("Unknown command '%s'", command.Name),
			}); err != nil {
				return err
			}
			// ( failure ( ( 210001 34:Unknown editor command 'no-existe' 0: 0 ) ) )
			// return fmt.Errorf("unknown command %q", command.Name)
		}
	}
}

func replyUnimplemented(conn conn, cmd string) error {
	return conn.WriteFailure(Error{
		AprErr:  210001,
		Message: fmt.Sprintf("Command '%s' unimplemented", cmd),
	})
}
