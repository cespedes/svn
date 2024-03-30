package svn

import (
	"io"
)

// SvnVersion is the SVN protocol version implemented in this package.
const SvnVersion = 2

// conn is a representation of a connection between a client and a server.
type conn struct {
	r io.Reader
	w io.Writer
	i *Itemizer
}

// Write converts "what" into an Item,
// if needed, and then sends it to the other end of the connection.
func (c *conn) Write(what any) error {
	item, err := Marshal(what)
	if err != nil {
		return nil
	}
	_, err = c.w.Write([]byte(item.String() + " "))
	return err
}

// Write converts "what" into an Item,
// if needed, and then sends it as a "success" frame
// to the other end of the connection.
func (c *conn) WriteSuccess(what any) error {
	return c.Write([]any{
		"success",
		what,
	})
}

// WriteFailure sends a "failure" frame
// to the other end of the connection.
func (c *conn) WriteFailure(err error) error {
	var msg struct {
		AprErr  int
		Message []byte
		File    []byte
		Line    int
	}
	msg.AprErr = 21005
	msg.Message = []byte(err.Error())
	if Err, ok := err.(Error); ok {
		msg.AprErr = Err.AprErr
		msg.Message = []byte(Err.Message)
		msg.File = []byte(Err.File)
		msg.Line = Err.Line
	}
	return c.Write([]any{
		"failure",
		[]any{msg},
	})
}

// Read reads an Item from the connection,
// and stores it in "where", converting its type if needed.
func (c *conn) Read(where any) error {
	if c.i == nil {
		c.i = NewItemizer(c.r)
	}
	item, err := c.i.Item()
	if err != nil {
		return err
	}
	return Unmarshal(item, where)
}

// ReadResponse reads from the connection,
// expecting a "command response".
// If the response is a "succcess",
// it stores it in "where", converting its type if needed.
func (c *conn) ReadResponse(where any) error {
	var item Item
	err := c.Read(&item)
	if err != nil {
		return err
	}
	resp, err := ParseResponse(item)
	if err != nil {
		return err
	}
	return Unmarshal(resp, where)
}

// Close closes the connection.
// It calls r.Close() and w.Close() if they are available.
func (c *conn) Close() error {
	if cr, ok := c.r.(io.Closer); ok {
		err := cr.Close()
		if err != nil {
			return err
		}
	}
	if cw, ok := c.w.(io.Closer); ok {
		err := cw.Close()
		if err != nil {
			return err
		}
	}
	c.i = nil
	return nil
}
