package svn

import (
	"io"
)

type Command struct {
	Name   string
	Params []Item
}

const (
	CommandLogin        = "greeting"
	CommandGetLatestRev = "get-latest-rev"
	CommandStat         = "stat"
)

type Greet struct {
	Version      int
	Capabilities []string
	URL          string
	RAClient     string
	Client       []string
}

// Serve creates a server that, once called, sends and receives SVN messages
// against a client, and issues a call to the callback function when
// a message is received.
//
// Serve returns if there is an error, or after the end of the connection.
func Serve(r io.Reader, w io.Writer, login func(Greet) error, callback func(Command, Conn) error) error {
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
		[]any{"edit-pipeline"},
	})
	if err != nil {
		return err
	}

	var greet Greet
	err = conn.Read(&greet)
	if err != nil {
		return err
	}
	if login != nil {
		err = login(greet)
		if err != nil {
			conn.WriteFailure(err)
			return err
		}
	}

	// Sending "auth-request":
	err = conn.WriteSuccess([]any{
		[]any{
			"ANONYMOUS",
			"EXTERNAL",
		},
		[]byte("c5a7a7b1-3e3e-4c98-a541-f46ece210564"),
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
		[]byte("c5a7a7b1-3e3e-4c98-a541-f46ece210564"),
		[]byte(greet.URL),
		[]any{},
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
		err = callback(command, conn)
		if err != nil {
			return err
		}
	}
}
