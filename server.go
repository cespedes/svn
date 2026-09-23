package svn

import (
	"crypto/md5"
	"fmt"
	"io"
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
	// (or at the latest revision, if rev is nil).
	Stat func(path string, rev *uint) (Dirent, error)

	// CheckPath answers a "check-path" command, returning the node kind
	// ("file", "dir" or "none") of path at rev (or at the latest revision,
	// if rev is nil).
	CheckPath func(path string, rev *uint) (string, error)

	// List answers a "list" command, returning the directory entries under
	// path at rev. depth is one of the protocol's depth words (e.g.
	// "immediates"), fields selects which optional Dirent fields the
	// client wants populated, and pattern, if non-empty, restricts the
	// result to entries matching one of the given glob patterns.
	List func(path string, rev *uint, depth string, fields []string, pattern []string) ([]Dirent, error)

	// GetFile answers a "get-file" command, returning the revision the
	// content came from, the file's properties (if wantProps), and its
	// content (if wantContents).
	GetFile func(path string, rev *uint, wantProps bool, wantContents bool) (uint, []PropList, []byte, error)

	// Log answers a "log" command, returning the log entries for paths
	// between startRev and endRev. changedPaths reports whether the
	// client asked for each LogEntry's Changed field to be populated.
	Log func(paths []string, startRev uint, endRev uint, changedPaths bool) ([]LogEntry, error)

	// Update is intended to answer an "update" command. It is currently
	// unused: Serve replies "unimplemented" when it is nil, but never
	// actually calls it when it is set, since the report/editor exchange
	// that would drive an update is not implemented yet.
	Update func(rev *uint, target string, recurse bool)

	// SetPath is intended to answer a "set-path" command, part of the
	// report mechanism used to drive an update. It is currently unused:
	// Serve replies "unimplemented" when it is nil, but never actually
	// calls it when it is set.
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
// Serve returns if there is an error, or after the end of the connection.
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
				if err = conn.WriteFailure(err); err != nil {
					return err
				}
				continue
			}
			if err = conn.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
				return err
			}
			// response: ( ? entry:dirent ) -- a list holding at most one
			// element, which is itself the dirent tuple.
			if err = conn.WriteSuccess([]any{[]any{
				entry.Kind,
				entry.Size,
				entry.HasProps,
				entry.CreatedRev,
				[]any{[]byte(entry.CreatedDate)},
				[]any{[]byte(entry.LastAuthor)},
			}}); err != nil {
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
			for _, d := range dirents {
				if err = conn.Write([]any{
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
