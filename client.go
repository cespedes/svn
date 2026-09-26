package svn

import (
	"fmt"
	"io"
	"io/fs"
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
	// Info holds the repository information (UUID, root URL, capabilities)
	// received from the server during Connect.
	Info ReposInfo
}

// Connect creates a [Client] and establishes a connection to a SVN server,
// using the scheme of the given address to find out how to reach it.
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

	if err := c.exec(execArgs[0], execArgs[1:]...); err != nil {
		return nil, err
	}

	if err := c.handshake(u.String()); err != nil {
		return nil, err
	}

	return &c, nil
}

// NewClient returns a [Client] that speaks the protocol over r and w,
// performing the same greeting/version-negotiation/auth handshake as
// [Connect], without spawning any subprocess. address identifies the
// repository (or a path within it) being requested, exactly as it would be
// given to Connect.
//
// This lets a caller supply its own transport: a connection dialed by
// hand, an in-memory pipe for testing, or (once this package supports
// svn:// directly) a raw TCP connection.
func NewClient(r io.Reader, w io.Writer, address string) (*Client, error) {
	c := &Client{conn: conn{r: r, w: w}}
	if err := c.handshake(address); err != nil {
		return nil, err
	}
	return c, nil
}

// handshake performs the greeting, version negotiation, auth and
// repos-info exchange that both Connect and NewClient need, once their
// underlying connection is established.
func (c *Client) handshake(address string) error {
	var greet struct {
		MinVer       int
		MaxVer       int
		Mechs        Item
		Capabilities []string
	}
	err := c.conn.ReadResponse(&greet)
	if err != nil {
		return fmt.Errorf("reading greeting: %w", err)
	}
	if greet.MinVer > SvnVersion || greet.MaxVer < SvnVersion {
		return fmt.Errorf("client: unsupported SVN version range (%d .. %d)", greet.MinVer, greet.MaxVer)
	}
	err = c.conn.Write([]any{
		SvnVersion,
		//[]string{"edit-pipeline", "svndiff1", "accepts-svndiff2", "absent-entries", "depth", "mergeinfo", "log-revprops"},
		[]string{"edit-pipeline"},
		[]byte(address),
		[]byte(SvnClient),
		[]any{},
	})
	if err != nil {
		return fmt.Errorf("client: sending greeting response: %w", err)
	}

	if err := c.handleAuth(); err != nil {
		return err
	}

	if err := c.conn.ReadResponse(&c.Info); err != nil {
		return fmt.Errorf("reading repos-info: %w", err)
	}

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

// chooseAuthMechanism picks which SASL mechanism to use given the list of
// mechanisms a server offered in an auth-request, preferring EXTERNAL (no
// credentials needed beyond the already-authenticated transport) and
// falling back to ANONYMOUS. It returns an error if the server offers
// neither, since this client does not implement any other mechanism.
func chooseAuthMechanism(offered []string) (string, error) {
	haveAnonymous := false
	for _, mech := range offered {
		if mech == "EXTERNAL" {
			return "EXTERNAL", nil
		}
		if mech == "ANONYMOUS" {
			haveAnonymous = true
		}
	}
	if haveAnonymous {
		return "ANONYMOUS", nil
	}
	return "", fmt.Errorf("client: no supported auth mechanism in %v", offered)
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
	mech, err := chooseAuthMechanism(authRequest.Mechanisms)
	if err != nil {
		return err
	}
	err = c.conn.Write([]any{
		mech,
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

// Stat sends a "stat" command, asking for the status of path at rev, or at
// the latest revision if rev is nil. If path does not exist at that
// revision, it returns an error satisfying errors.Is(err, fs.ErrNotExist).
func (c *Client) Stat(path string, rev *int) (Stat, error) {
	lrev := []int{}
	if rev != nil {
		lrev = append(lrev, *rev)
	}
	input := []any{[]byte(path), lrev}

	// The response is "( ? entry:dirent )". A "?"/optional marker always
	// wraps whatever it marks in its own 0-or-1-element list; since
	// "entry" here is itself a compound dirent tuple (which is naturally
	// its own list), a present entry ends up nested two levels deep:
	// ( ( ( kind size has-props created-rev [date] [author] ) ) ).
	// Confirmed against a real svnserve's own wire response, which sends
	// exactly this shape -- unmarshaling straight into a Stat, or even
	// into a single level of 0-or-1-element list, only ever fills the
	// first field.
	raw, err := sendCommand[Item](c, "stat", input)
	if err != nil {
		return Stat{}, err
	}
	if len(raw.List) == 0 || len(raw.List[0].List) == 0 {
		return Stat{}, fmt.Errorf("stat: %q: %w", path, fs.ErrNotExist)
	}
	var stat Stat
	if err := Unmarshal(raw.List[0].List[0], &stat); err != nil {
		return Stat{}, fmt.Errorf("stat: %q: %w", path, err)
	}
	return stat, nil
}

// List sends a "list" command, asking for the entries of directory path at
// rev (or at the latest revision, if rev is nil). depth is one of the
// protocol's depth words (e.g. "immediates" for just the direct children,
// "infinity" for the full subtree). fields selects which optional Dirent
// fields to populate (e.g. "size", "created-rev", "time", "last-author").
// Dirent.Kind is always present in the result, but comes back as "unknown"
// unless "kind" is included in fields too.
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

// GetFile sends a "get-file" command, asking for the properties and/or
// contents of the file at path and rev (or at the latest revision, if rev
// is nil). The server is expected to return properties only if wantProps
// is true; content is read and returned in full only if wantContent is
// true, otherwise the returned []byte is nil.
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

	// The protocol sends a second, empty command response after the content
	// terminator, to report whether an error occurred while sending the
	// file. It must be consumed here, or it will desync the connection for
	// whatever command runs next.
	var final Item
	if err = c.conn.ReadResponse(&final); err != nil {
		return nil, nil, fmt.Errorf("GetFile: reading final response: %w", err)
	}

	return response.Props, content, nil
}

//  log
//    params:   ( ( target-path:string ... ) [ start-rev:number ]
//                [ end-rev:number ] changed-paths:bool strict-node:bool
//                ? limit:number
//                ? include-merged-revisions:bool
//                all-revprops | revprops ( revprop:string ... ) )

// Log sends a "log" command, asking for the log entries between startRev
// and endRev. A nil paths defaults to the repository root; a nil startRev
// means the latest revision, and a nil endRev means revision 0 (the
// beginning of history) -- so the zero value of all three arguments asks
// for the full history of the repository root. changedPaths reports
// whether each returned LogEntry.Changed should be populated.
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
