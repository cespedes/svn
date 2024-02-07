package svn

import (
	"fmt"
	"io"
)

// A Client is a SVN client.  Its zero value is not usable; it has to be
// connected to a server using [NewClient].
type Client struct {
	r io.Reader
	w io.Writer
	i *Itemizer
}

// NewClient initiaties a connection to a SVN server and returns a [Client]
// ready to be used.
func NewClient(r io.Reader, w io.Writer) (*Client, error) {
	c := &Client{
		r: r,
		w: w,
	}
	c.i = NewItemizer(r)

	item, err := c.i.Item()
	if err != nil {
		return c, err
	}
	fmt.Printf("greeting: %s\n", item)
	return c, nil
}
