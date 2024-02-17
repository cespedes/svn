package svn

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"os/exec"
)

const (
	SvnVersion = 2
	SvnClient  = "GoSVN/0.0.0"
)

type Conn struct {
	r io.Reader
	w io.Writer
	i *Itemizer
}

// A Client is a SVN client.  Its zero value is not usable; it has to be
// connected to a server using [Client.Connect].
type Client struct {
	conn Conn
	cmd  *exec.Cmd
}

// NewClient returns an empty [Client]
func NewClient() *Client {
	return &Client{}
}

// Connect creates a [Client] and establishes a connection
// to a SVN server, using the given URL to find out know how to connect to it.
func Connect(url string) (*Client, error) {
	c := NewClient()
	return c, c.Connect(url)
}

// Connect establishes a connection to a SVN server, using the given address
// to find out know how to connect to it.
//
// Right now, it works only with "file" URLs, invoking "svnserve -t" to connect
// to a server.
func (c *Client) Connect(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("svn connect: parsing %q: %w", address, err)
	}

	// schema could be one of:
	// - file
	// - http
	// - https
	// - svn
	// - svn+ssh
	switch u.Scheme {
	case "file":
		u.Scheme = "svn"
	default:
		return fmt.Errorf("svn: connect to %q: scheme %q not implemented", address, u.Scheme)
	}

	err = c.exec("svnserve", "-t")
	if err != nil {
		return err
	}

	var greet struct {
		MinVer       int
		MaxVer       int
		Mechs        Item
		Capabilities []string
	}
	err = c.conn.ReadResponse(&greet)
	if err != nil {
		return fmt.Errorf("reading greeting: %w", err)
	}
	log.Printf("greeting: %+v\n", greet)
	if greet.MinVer > SvnVersion || greet.MaxVer < SvnVersion {
		return fmt.Errorf("unsupported SVN version range (%d .. %d)", greet.MinVer, greet.MaxVer)
	}
	err = c.conn.Write([]any{
		SvnVersion,
		//[]string{"edit-pipeline", "svndiff1", "accepts-svndiff2", "absent-entries", "depth", "mergeinfo", "log-revprops"},
		[]string{"edit-pipeline"},
		[]byte(u.String()),
		[]byte(SvnClient),
		[]any{},
	})
	if err != nil {
		return fmt.Errorf("sending greeting response: %w", err)
	}

	var authRequest struct {
		Mechanisms []string
		Realm      string
	}
	err = c.conn.ReadResponse(&authRequest)
	if err != nil {
		return fmt.Errorf("reading auth-request: %w", err)
	}
	log.Printf("auth-request: %+v\n", authRequest)
	err = c.conn.Write([]any{
		"EXTERNAL",
		[]any{
			[]byte{},
		},
	})
	if err != nil {
		return fmt.Errorf("sending auth response: %w", err)
	}
	var item Item
	err = c.conn.ReadResponse(&item)
	if err != nil {
		return fmt.Errorf("reading auth response: %w", err)
	}

	var reposInfo struct {
		UUID         string
		URL          string
		Capabilities []string
	}
	err = c.conn.ReadResponse(&reposInfo)
	if err != nil {
		return fmt.Errorf("reading repos-info: %w", err)
	}
	log.Printf("repos-info: %+v\n", reposInfo)

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
	c.conn.r = stdout
	c.conn.w = stdin
	return c.cmd.Start()
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
