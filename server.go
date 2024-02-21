package svn

import (
	"io"
)

// Serve creates a server that, once called, sends and receives SVN messages
// against a client, and issues a call to the callback function when
// a message is received.
func Serve(r io.Reader, w io.Writer, callback func(Item, Conn) error) error {
	conn := Conn{
		r: r,
		w: w,
	}

	var err error

	err = conn.WriteSuccess([]any{
		SvnVersion,
		SvnVersion,
		[]any{},
		[]any{"edit-pipeline"},
	})
	if err != nil {
		return err
	}
	for {
		var item Item
		err := conn.Read(&item)
		if err != nil {
			conn.Close()
			return err
		}
		err = callback(item, conn)
		if err != nil {
			conn.Close()
			return err
		}
	}
}
