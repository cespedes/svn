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

// Serve creates a server that, once called, sends and receives SVN messages
// against a client, and issues a call to the callback function when
// a message is received.
//
// Serve returns if there is an error, or after the end of the connection.
func Serve(r io.Reader, w io.Writer, callback func(Command, Item, Conn) error) error {
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

	var greet struct {
		Version      int
		Capabilities []string
		URL          string
		RAClient     string
		Client       []string
	}

	err = conn.Read(&item)
	if err != nil {
		return err
	}
	err = Unmarshal(item, &greet)
	if err != nil {
		return err
	}
	err = callback(Command{
		Name:   "greeting",
		Params: item.List,
	}, item, conn)

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
		err = callback(command, item, conn)
		if err != nil {
			return err
		}
	}
}
