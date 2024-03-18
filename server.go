package svn

import (
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
}

//		Version      int
//		Capabilities []string
//		URL          string
//		RAClient     string
//		Client       []string

type Command struct {
	Name   string
	Params []Item
}

// Serve sends and receives SVN messages against a client,
// issuing calls to the respective functions when a message
// is received.
//
// Serve returns if there is an error, or after the end of the connection.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	conn := Conn{
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
		var command Command
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
			if s.Stat == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			if len(command.Params) < 1 || command.Params[0].Type != StringType {
				conn.WriteFailure(Error{
					AprErr:  210004,
					Message: "Malformed network data",
				})
				continue
			}
			var rev *uint
			if len(command.Params) > 1 && command.Params[1].Type == NumberType {
				rev = &command.Params[1].Number
			}
			entry, err := s.Stat(command.Params[0].Text, rev)
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
		case "check-path":
			if s.CheckPath == nil {
				replyUnimplemented(conn, command.Name)
				continue
			}
			if len(command.Params) < 1 || command.Params[0].Type != StringType {
				conn.WriteFailure(Error{
					AprErr:  210004,
					Message: "Malformed network data",
				})
				continue
			}
			var rev *uint
			if len(command.Params) > 1 && command.Params[1].Type == NumberType {
				rev = &command.Params[1].Number
			}
			kind, err := s.CheckPath(command.Params[0].Text, rev)
			if err != nil {
				conn.WriteFailure(err)
				continue
			}
			conn.WriteSuccess([]any{[]any{}, []byte{}})
			conn.WriteSuccess([]any{kind})
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

func replyUnimplemented(conn Conn, cmd string) {
	conn.WriteFailure(Error{
		AprErr:  210001,
		Message: fmt.Sprintf("Command '%s' unimplemented", cmd),
	})
}
