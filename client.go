package svn

import (
	"fmt"
	"net/url"
	"os/exec"
)

// SvnClient is the SVN client string to send to servers.
const SvnClient = "GoSVN/0.0.0"

// A Client is a SVN client.  Its zero value is not usable: you will have
// to create it and connect it to a server using [Connect].
type Client struct {
	conn conn
	cmd  *exec.Cmd
	Info ReposInfo
}

// Connect creates a [Client] and establishes a connection
// to a SVN server, using the given address
// to find out know how to connect to it.
//
// Right now, it works only with "file" and "svn+ssh" URLs,
// invoking "svnserve -t" (locally or remotely) to connect
// to a server
func Connect(address string) (*Client, error) {
	var c Client

	u, err := url.Parse(address)
	if err != nil {
		return nil, fmt.Errorf("svn connect: parsing %q: %w", address, err)
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
		return nil, fmt.Errorf("svn: connect to %q: scheme %q not implemented", address, u.Scheme)
	}

	err = c.exec(execArgs[0], execArgs[1:]...)
	if err != nil {
		return nil, err
	}

	var greet struct {
		MinVer       int
		MaxVer       int
		Mechs        Item
		Capabilities []string
	}
	err = c.conn.ReadResponse(&greet)
	if err != nil {
		return nil, fmt.Errorf("reading greeting: %w", err)
	}
	if greet.MinVer > SvnVersion || greet.MaxVer < SvnVersion {
		return nil, fmt.Errorf("client: unsupported SVN version range (%d .. %d)", greet.MinVer, greet.MaxVer)
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
		return nil, fmt.Errorf("client: sending greeting response: %w", err)
	}

	err = c.handleAuth()
	if err != nil {
		return nil, err
	}

	err = c.conn.ReadResponse(&c.Info)
	if err != nil {
		return nil, fmt.Errorf("reading repos-info: %w", err)
	}

	return &c, nil
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
		return fmt.Errorf("reading auth-request: %w", err)
	}
	if len(authRequest.Mechanisms) == 0 {
		return nil
	}
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

// List sends a "list" command, asking for list of files.
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

//  get-file
//    params:   ( path:string [ rev:number ] want-props:bool want-contents:bool
//                ? want-iprops:bool )
//    response: ( [ checksum:string ] rev:number props:proplist
//                [ inherited-props:iproplist ] )

// GetFile sends a "get-file" command, asking for the contents of a file.
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
		return nil, nil, fmt.Errorf("GetFile: %w", err)
	}

	if !wantContent {
		return response.Props, nil, nil
	}
	content := []byte{}
	for {
		var b []byte
		err = c.conn.Read(&b)
		if err != nil {
			return nil, nil, fmt.Errorf("GetFile: reading content: %w", err)
		}
		if len(b) == 0 {
			break
		}
		content = append(content, b...)
	}

	return response.Props, content, nil
}

//  log
//    params:   ( ( target-path:string ... ) [ start-rev:number ]
//                [ end-rev:number ] changed-paths:bool strict-node:bool
//                ? limit:number
//                ? include-merged-revisions:bool
//                all-revprops | revprops ( revprop:string ... ) )

// write(4, "( log ( ( 0: ) ( 22261 ) ( 0 ) false false 0 false revprops ( 10:svn:author 8:svn:date 7:svn:log ) ) ) ", 103) = 103
// Log sends a "log" command, asking for log entries.
func (c *Client) Log(paths []string, startRev *int, endRev *int, changedPaths bool) ([]LogEntry, error) {
	srev := []int{}
	if startRev != nil {
		srev = append(srev, *startRev)
	}
	erev := []int{}
	if endRev == nil {
		erev = append(erev, 0)
	} else {
		erev = append(erev, *endRev)
	}
	if len(paths) == 0 {
		paths = append(paths, "")
	}
	var bpaths [][]byte
	for _, p := range paths {
		bpaths = append(bpaths, []byte(p))
	}

	err := c.conn.Write([]any{
		"log", []any{
			bpaths,
			srev,
			erev,
			changedPaths,
			false, 0, false, "revprops", []any{
				[]byte("svn:author"),
				[]byte("svn:date"),
				[]byte("svn:log"),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("client: sending \"log\": %w", err)
	}
	if err = c.handleAuth(); err != nil {
		return nil, fmt.Errorf("client: Log: auth: %w", err)
	}

	var entries []LogEntry
	for {
		var item Item
		err = c.conn.Read(&item)
		if err != nil {
			return nil, fmt.Errorf("client: Log: reading log entriy: %w", err)
		}
		if item.Type == WordType && item.Text == "done" {
			break
		}
		var entry LogEntry
		err = Unmarshal(item, &entry)
		if err != nil {
			return nil, fmt.Errorf("client: Log: unmarshaling dirent entry: %w", err)
		}
		entries = append(entries, entry)
	}
	var item Item
	err = c.conn.ReadResponse(&item)
	if err != nil {
		return nil, fmt.Errorf("client: Log: reading final response: %w", err)
	}
	return entries, nil
}
