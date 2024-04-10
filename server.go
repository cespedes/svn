package svn

import (
	"crypto/md5"
	"fmt"
	"io"
)

// A Server defines parameters for running a SVN server.
type Server struct {
	ReposInfo    ReposInfo
	Greet        func(version int, capabilities []string, url string, raclient string, client *string) (ReposInfo, error)
	GetLatestRev func() (int, error)
	Stat         func(path string, rev *uint) (Dirent, error)
	CheckPath    func(path string, rev *uint) (string, error)
	List         func(path string, rev *uint, depth string, fields []string, pattern []string) ([]Dirent, error)
	GetFile      func(path string, rev *uint, wantProps bool, wantContents bool) (uint, []PropList, []byte, error)
	Log          func(paths []string, startRev uint, endRev uint) ([]LogEntry, error)
	Update       func(rev *uint, target string, recurse bool)
	SetPath      func(path string, rev uint, startEmpty bool)
	FinishReport func()
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
				replyUnimplemented(conn, command.Name)
				continue
			}
			rev, err := s.GetLatestRev()
			if err != nil {
				conn.WriteFailure(err)
				continue
			}
			// empty auth-request:
			conn.WriteSuccess([]any{[]any{}, []byte{}})
			conn.WriteSuccess([]any{rev})
		case "stat":
			// params: ( path:string [ rev:number ] )
			if s.Stat == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			var args struct {
				Path string
				Rev  *uint
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				conn.WriteFailure(neterr)
				continue
			}
			entry, err := s.Stat(args.Path, args.Rev)
			if err != nil {
				conn.WriteFailure(err)
				continue
			}
			conn.WriteSuccess([]any{[]any{}, []byte{}})
			conn.WriteSuccess([]any{[]any{[]any{
				entry.Kind,
				entry.Size,
				entry.HasProps,
				entry.CreatedRev,
				[]any{[]byte(entry.CreatedDate)},
				[]any{[]byte(entry.LastAuthor)},
			}}})
		case "list":
			// params: ( path:string [ rev:number ] depth:word ( field:dirent-field ... ) ? ( pattern:string ... ) )
			if s.List == nil {
				replyUnimplemented(conn, command.Name)
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
				conn.WriteFailure(neterr)
				continue
			}
			dirents, err := s.List(args.Path, args.Rev, args.Depth, args.Fields, args.Pattern)
			if err != nil {
				conn.WriteFailure(err)
				continue
			}
			conn.WriteSuccess([]any{[]any{}, []byte{}})
			for _, d := range dirents {
				conn.Write([]any{
					[]byte(d.Path),
					d.Kind,
					[]any{d.Size},
					[]any{d.HasProps},
					[]any{d.CreatedRev},
					[]any{[]byte(d.CreatedDate)},
					[]any{[]byte(d.LastAuthor)},
				})
			}
			conn.Write("done")
			conn.WriteSuccess([]any{})
		case "check-path":
			// params: ( path:string [ rev:number ] )
			if s.CheckPath == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			var args struct {
				Path string
				Rev  *uint
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				conn.WriteFailure(neterr)
				continue
			}
			kind, err := s.CheckPath(args.Path, args.Rev)
			if err != nil {
				conn.WriteFailure(err)
				continue
			}
			conn.WriteSuccess([]any{[]any{}, []byte{}})
			conn.WriteSuccess([]any{kind})
		case "get-file":
			// params: ( path:string [ rev:number ] want-props:bool want-contents:bool ? want-iprops:bool )
			if s.GetFile == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			var args struct {
				Path         string
				Rev          *uint
				WantProps    bool
				WantContents bool
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				conn.WriteFailure(neterr)
				continue
			}
			rev, proplist, contents, err := s.GetFile(args.Path, args.Rev, args.WantProps, args.WantContents)
			if err != nil {
				conn.WriteFailure(err)
				continue
			}
			checksum := []byte(fmt.Sprintf("%x", md5.Sum(contents)))
			conn.WriteSuccess([]any{[]any{}, []byte{}})
			conn.WriteSuccess([]any{[]any{checksum}, rev, proplist})
			if args.WantContents {
				conn.Write(contents)
				conn.Write([]byte{})
				conn.WriteSuccess([]any{})
			}
		case "log":
			// params: ( ( target-path:string ... ) [ start-rev:number ] [ end-rev:number ] changed-paths:bool strict-node:bool ? limit:number ? include-merged-revisions:bool all-revprops | revprops ( revprop:string ... ) )
			if s.Log == nil {
				replyUnimplemented(conn, command.Name)
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
				conn.WriteFailure(neterr)
				continue
			}
			logEntries, err := s.Log(args.Paths, args.StartRev, args.EndRev)
			if err != nil {
				conn.WriteFailure(err)
				continue
			}
			conn.WriteSuccess([]any{[]any{}, []byte{}})
			for _, l := range logEntries {
				// ( ( ) 7573 ( 3:noc ) ( 27:2024-04-02T13:37:34.350221Z ) ( 43:New open position: 2024-04-phd-visiting-apt ) false false 0 ( ) false )
				conn.Write([]any{
					l.Changed,
					l.Rev,
					[]any{[]byte(l.Author)},
					[]any{[]byte(l.Date)},
					[]any{[]byte(l.Message)},
				})
			}
			conn.Write("done")
			conn.WriteSuccess([]any{})
		case "update":
			if s.Update == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			var args struct {
				Rev     *uint
				Target  string
				Recurse bool
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				conn.WriteFailure(neterr)
				continue
			}
			// empty auth-request:
			conn.WriteSuccess([]any{[]any{}, []byte{}})
		case "set-path": // From the Report Command Set
			if s.SetPath == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			var args struct {
				Path       string
				Rev        uint
				StartEmpty bool
			}
			if err = Unmarshal(command.Params, &args); err != nil {
				conn.WriteFailure(neterr)
				continue
			}
			// no response in set-path
		case "finish-report": // From the Report Command Set
			if s.FinishReport == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			// no response?
			conn.WriteSuccess([]any{[]any{}, []byte{}})
		default:
			conn.WriteFailure(Error{
				AprErr:  210001,
				Message: fmt.Sprintf("Unknown command '%s'", command.Name),
			})
			// ( failure ( ( 210001 34:Unknown editor command 'no-existe' 0: 0 ) ) )
			// return fmt.Errorf("unknown command %q", command.Name)
		}
	}
}

func replyUnimplemented(conn conn, cmd string) {
	conn.WriteFailure(Error{
		AprErr:  210001,
		Message: fmt.Sprintf("Command '%s' unimplemented", cmd),
	})
}
