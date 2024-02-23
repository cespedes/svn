package svn

import (
	"io"
)

const SvnVersion = 2

// Conn is a representation of a connection between a client and a server.
type Conn struct {
	r io.Reader
	w io.Writer
	i *Itemizer
}

// Write converts "what" into an Item,
// if needed, and then sends it to the other end of the connection.
func (c *Conn) Write(what any) error {
	item, err := Marshal(what)
	if err != nil {
		return nil
	}
	_, err = c.w.Write([]byte(item.String() + " "))
	return err
}

func (c *Conn) WriteSuccess(what any) error {
	return c.Write([]any{
		"success",
		what,
	})
}

func (c *Conn) WriteFailure(what error) error {
	return c.Write([]any{
		"failure",
		[]any{
			[]any{
				21005,
				[]byte(what.Error()),
				[]byte{},
				0,
			},
		},
	})
}

// Read reads an Item from the connection,
// and stores it in "where", converting its type if needed.
func (c *Conn) Read(where any) error {
	if c.i == nil {
		c.i = NewItemizer(c.r)
	}
	item, err := c.i.Item()
	if err != nil {
		return err
	}
	return Unmarshal(item, where)
}

func (c *Conn) ReadResponse(where any) error {
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
func (c *Conn) Close() error {
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
