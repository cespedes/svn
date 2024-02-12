package svn

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"os/exec"
)

// A Client is a SVN client.  Its zero value is not usable; it has to be
// connected to a server using [NewClient].
type Client struct {
	r   io.Reader
	w   io.Writer
	i   *Itemizer
	url string
	cmd *exec.Cmd
}

// NewClient returns an empty [Client]
func NewClient() (*Client, error) {
	return &Client{}, nil
}

// Connect creates a [Client] and establishes a connection
// to a SVN server, using the given URL to find out know how to connect to it.
func Connect(url string) (*Client, error) {
	c, err := NewClient()
	if err != nil {
		return c, err
	}
	return c, c.Connect(url)
}

// Connect establishes a connection to a SVN server, using the given address
// to find out know how to connect to it.
func (c *Client) Connect(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("svn connect: parsing %q: %w", address, err)
	}

	// schema can be one of:
	// - file
	// - http
	// - https
	// - svn
	// - svn+ssh
	switch u.Scheme {
	case "file":
	default:
		return fmt.Errorf("svn: connect to %q: scheme %q not implemented", address, u.Scheme)
	}

	err = c.exec("svnserve", "-t")
	if err != nil {
		return err
	}

	greet, err := c.i.Item()
	if err != nil {
		return err
	}
	params, err := ParseResponse(greet)
	if err != nil {
		return err
	}
	log.Printf("greeting: %s\n", params)
	return nil
}

func (c *Client) exec(name string, arg ...string) error {
	c.cmd = exec.Command(name, arg...)
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return err
	}
	c.r = stdout
	c.w = stdin
	c.i = NewItemizer(c.r)
	return c.cmd.Start()
}
