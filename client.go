package svn

import (
	"fmt"
	"net/url"
	"os/exec"
)

const SvnClient = "GoSVN/0.0.0"

// A Client is a SVN client.  Its zero value is not usable; it has to be
// connected to a server using [Client.Connect].
type Client struct {
	conn Conn
	cmd  *exec.Cmd
	Info struct {
		UUID         string
		URL          string
		Capabilities []string
	}
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

	var execArgs []string

	// schema could be one of:
	// - file
	// - http
	// - https
	// - svn
	// - svn+ssh
	switch u.Scheme {
	case "file":
		execArgs = []string{
			"svnserve",
			"-t",
		}
		// Standard "svnserve" does not work if we tell it we want a "file:" scheme:
		u.Scheme = "svn+ssh"
	case "svn+ssh":
		host := u.Host
		if u.User != nil {
			host = u.User.String() + "@" + host
		}
		execArgs = []string{
			"ssh",
			"-q",
			"-o",
			"ControlMaster=no",
			"--",
			host,
			"svnserve",
			"-t",
		}
	default:
		return fmt.Errorf("svn: connect to %q: scheme %q not implemented", address, u.Scheme)
	}

	err = c.exec(execArgs[0], execArgs[1:]...)
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
	// log.Printf("client: greeting: %+v\n", greet)
	if greet.MinVer > SvnVersion || greet.MaxVer < SvnVersion {
		return fmt.Errorf("client: unsupported SVN version range (%d .. %d)", greet.MinVer, greet.MaxVer)
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
		return fmt.Errorf("client: sending greeting response: %w", err)
	}

	err = c.handleAuth()
	if err != nil {
		return err
	}

	err = c.conn.ReadResponse(&c.Info)
	if err != nil {
		return fmt.Errorf("reading repos-info: %w", err)
	}
	// log.Printf("repos-info: %+v\n", reposInfo)

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

func (c *Client) handleAuth() error {
	var authRequest struct {
		Mechanisms []string
		Realm      string
	}
	err := c.conn.ReadResponse(&authRequest)
	if err != nil {
		return fmt.Errorf("client: reading auth-request: %w", err)
	}
	if len(authRequest.Mechanisms) == 0 {
		return nil
	}
	// log.Printf("auth-request: %+v\n", authRequest)
	err = c.conn.Write([]any{
		"EXTERNAL",
		[]any{
			[]byte{},
		},
	})
	if err != nil {
		return fmt.Errorf("client: sending auth response: %w", err)
	}
	var item Item
	err = c.conn.ReadResponse(&item)
	if err != nil {
		return fmt.Errorf("client: reading auth response: %w", err)
	}
	return nil
}

func sendCommand[Output any](c *Client, cmd string, params any) (Output, error) {
	var out Output

	err := c.conn.Write([]any{
		cmd,
		params,
	})
	if err != nil {
		return out, fmt.Errorf("client: sending %s: %w", cmd, err)
	}
	if err = c.handleAuth(); err != nil {
		return out, err
	}

	err = c.conn.ReadResponse(&out)
	return out, err
}

// GetLatestRev sends a "get-latest-rev" command, asking for
// the latest revision number in the repository.
func (c *Client) GetLatestRev() (int, error) {
	return sendCommand[int](c, "get-latest-rev", []any{})
}

type Stat struct {
	Kind        string
	Size        uint64
	HasProps    bool
	CreatedRev  uint
	CreatedDate string
	LastAuthor  string
}

// Stat sends a "stat" command, asking for the status of a path in a revision.
// "rev" can be nil or a pointer to an integer.
func (c *Client) Stat(path string, rev *int) (Stat, error) {
	lrev := []int{}
	if rev != nil {
		lrev = append(lrev, *rev)
	}
	input := []any{[]byte(path), lrev}

	return sendCommand[Stat](c, "stat", input)
}

type Dirent struct {
	Path        string
	Kind        string
	Size        uint64
	HasProps    bool
	CreatedRev  uint
	CreatedDate string
	LastAuthor  string
}

// List sends a "list" command, asking for list of files
func (c *Client) List(path string, rev *int, depth string, fields []string) ([]Dirent, error) {
	lrev := []int{}
	if rev != nil {
		lrev = append(lrev, *rev)
	}
	params := []any{
		[]byte(path),
		lrev,
		depth,
		fields,
	}
	err := c.conn.Write([]any{
		"list",
		params,
	})
	if err != nil {
		return nil, fmt.Errorf("client: sending \"list\": %w", err)
	}
	if err = c.handleAuth(); err != nil {
		return nil, fmt.Errorf("client: List: auth: %w", err)
	}

	var dirents []Dirent
	for {
		var item Item
		err = c.conn.Read(&item)
		if err != nil {
			return nil, fmt.Errorf("client: List: reading dirent entry: %w", err)
		}
		if item.Type == WordType && item.Text == "done" {
			break
		}
		var dirent Dirent
		err = Unmarshal(item, &dirent)
		if err != nil {
			return nil, fmt.Errorf("client: List: unmarshaling dirent entry: %w", err)
		}
		dirents = append(dirents, dirent)
	}
	var item Item
	err = c.conn.ReadResponse(&item)
	if err != nil {
		return nil, fmt.Errorf("client: List: reading final response: %w", err)
	}
	return dirents, nil
}

type PropList struct {
	Name  string
	Value string
}

// GetFile sends a "get-file" command, asking for the contents of a file
func (c *Client) GetFile(path string, rev *int, wantProps bool, wantContent bool) ([]PropList, []byte, error) {
	lrev := []int{}
	if rev != nil {
		lrev = append(lrev, *rev)
	}
	type FileResponse struct {
		Checksum string
		Rev      int
		Props    []PropList
	}
	response, err := sendCommand[FileResponse](c, "get-file", []any{
		[]byte(path),
		lrev,
		wantProps,
		wantContent,
		"false",
	})

	if err != nil {
		return nil, nil, fmt.Errorf("client: GetFile: %w", err)
	}

	if !wantContent {
		return response.Props, nil, nil
	}
	content := []byte{}
	for {
		var b []byte
		err = c.conn.Read(&b)
		if err != nil {
			return nil, nil, fmt.Errorf("client: GetFile: reading content: %w", err)
		}
		if len(b) == 0 {
			break
		}
		content = append(content, b...)
	}

	// CLIENT: ( get-file ( 0: ( 1 ) true false  false ) )
	return response.Props, content, nil
}
