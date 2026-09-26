package svn

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
)

// newTestClient returns a Client wired to one end of an in-memory duplex
// connection, and the other end for a test to act as the server on.
func newTestClient() (*Client, net.Conn) {
	clientSide, serverSide := net.Pipe()
	c := &Client{conn: conn{r: clientSide, w: clientSide}}
	return c, serverSide
}

func TestClientStatUnmarshalsAllFields(t *testing.T) {
	c, server := newTestClient()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		sc := conn{r: server, w: server}

		// Consume the "stat" command sent by the client.
		var item Item
		if err := sc.Read(&item); err != nil {
			done <- err
			return
		}
		// Empty auth-request: no authentication needed for this command.
		if err := sc.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
			done <- err
			return
		}
		// The actual "stat" response, shaped exactly like server.go's own
		// "stat" case (and confirmed against a real svnserve): the dirent
		// tuple nested two levels deep inside the optional-entry marker.
		if err := sc.WriteSuccess([]any{[]any{[]any{
			"file", uint64(1234), true, uint(42),
			[]any{[]byte("2024-04-02T13:37:34.350221Z")},
			[]any{[]byte("juan")},
		}}}); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	stat, err := c.Stat("trunk/foo.txt", nil)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	want := Stat{
		Kind:        "file",
		Size:        1234,
		HasProps:    true,
		CreatedRev:  42,
		CreatedDate: "2024-04-02T13:37:34.350221Z",
		LastAuthor:  "juan",
	}
	if stat != want {
		t.Errorf("Stat() = %+v, want %+v", stat, want)
	}

	if err := <-done; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

func TestClientStatNoEntryReturnsError(t *testing.T) {
	c, server := newTestClient()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		sc := conn{r: server, w: server}
		var item Item
		if err := sc.Read(&item); err != nil {
			done <- err
			return
		}
		if err := sc.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
			done <- err
			return
		}
		// No entry: the path does not exist.
		if err := sc.WriteSuccess([]any{}); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	if _, err := c.Stat("does/not/exist", nil); err == nil {
		t.Errorf("Stat: expected error, got none")
	}

	if err := <-done; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

// TestClientStatMatchesRealSvnserveWire replays a byte-for-byte capture of
// a real svnserve's response to "( stat ( 0: ( ) ) )", reported against a
// directory. It pins down the exact nesting real-world servers use, as
// opposed to a hand-built approximation of it.
func TestClientStatMatchesRealSvnserveWire(t *testing.T) {
	c, server := newTestClient()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		var item Item
		if err := (&conn{r: server}).Read(&item); err != nil {
			done <- err
			return
		}
		if _, err := server.Write([]byte("( success ( ( ) 0: ) ) ")); err != nil {
			done <- err
			return
		}
		if _, err := server.Write([]byte("( success ( ( ( dir 18446744073709551615 false 25416 ( 27:2026-09-23T11:59:39.809149Z ) ( 8:jane.doe ) ) ) ) ) ")); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	stat, err := c.Stat("", nil)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	want := Stat{
		Kind:        "dir",
		Size:        18446744073709551615, // svnserve's SVN_INVALID_FILESIZE, for a directory
		HasProps:    false,
		CreatedRev:  25416,
		CreatedDate: "2026-09-23T11:59:39.809149Z",
		LastAuthor:  "jane.doe",
	}
	if stat != want {
		t.Errorf("Stat() = %+v, want %+v", stat, want)
	}

	if err := <-done; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

// TestClientGetFileConsumesFinalResponse checks that GetFile reads the
// second, empty command response the protocol sends after the content
// terminator. If it didn't, that response would be left on the wire and
// would desync whatever command runs next on the same connection -- which
// is exactly what this test would catch, since it issues a GetLatestRev
// right after and expects a clean, correct answer.
func TestClientGetFileConsumesFinalResponse(t *testing.T) {
	c, server := newTestClient()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		sc := conn{r: server, w: server}

		// "get-file" exchange.
		var item Item
		if err := sc.Read(&item); err != nil {
			done <- err
			return
		}
		if err := sc.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
			done <- err
			return
		}
		if err := sc.WriteSuccess([]any{[]any{}, 1, []any{}}); err != nil {
			done <- err
			return
		}
		if err := sc.Write([]byte("hello")); err != nil {
			done <- err
			return
		}
		if err := sc.Write([]byte{}); err != nil {
			done <- err
			return
		}
		if err := sc.WriteSuccess([]any{}); err != nil {
			done <- err
			return
		}

		// A second, unrelated command right after: if GetFile left the
		// final response above unread, this exchange would desync.
		if err := sc.Read(&item); err != nil {
			done <- err
			return
		}
		if err := sc.WriteSuccess([]any{[]any{}, []byte{}}); err != nil {
			done <- err
			return
		}
		if err := sc.WriteSuccess([]any{42}); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	_, content, err := c.GetFile("trunk/foo.txt", nil, true, true)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("content = %q, want %q", content, "hello")
	}

	rev, err := c.GetLatestRev()
	if err != nil {
		t.Fatalf("GetLatestRev after GetFile: %v", err)
	}
	if rev != 42 {
		t.Errorf("GetLatestRev() = %d, want 42", rev)
	}

	if err := <-done; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

func TestChooseAuthMechanism(t *testing.T) {
	cases := []struct {
		desc    string
		offered []string
		want    string
		wantErr bool
	}{
		{"prefers EXTERNAL", []string{"ANONYMOUS", "EXTERNAL"}, "EXTERNAL", false},
		{"falls back to ANONYMOUS", []string{"ANONYMOUS"}, "ANONYMOUS", false},
		{"only EXTERNAL offered", []string{"EXTERNAL"}, "EXTERNAL", false},
		{"unsupported mechanism only", []string{"CRAM-MD5"}, "", true},
		{"nothing offered", nil, "", true},
	}
	for _, tt := range cases {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := chooseAuthMechanism(tt.offered)
			if tt.wantErr {
				if err == nil {
					t.Errorf("chooseAuthMechanism(%v): expected error, got mechanism %q", tt.offered, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("chooseAuthMechanism(%v): unexpected error: %v", tt.offered, err)
			}
			if got != tt.want {
				t.Errorf("chooseAuthMechanism(%v) = %q, want %q", tt.offered, got, tt.want)
			}
		})
	}
}

// TestClientHandleAuthUsesOfferedMechanism checks that the client picks a
// mechanism the server actually offered (ANONYMOUS here, no EXTERNAL)
// instead of hardcoding one, by having the fake server reject anything but
// ANONYMOUS.
func TestClientHandleAuthUsesOfferedMechanism(t *testing.T) {
	c, server := newTestClient()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		sc := conn{r: server, w: server}
		if err := sc.WriteSuccess([]any{
			[]any{"ANONYMOUS"},
			[]byte("realm"),
		}); err != nil {
			done <- err
			return
		}
		// The token that follows the mechanism word is ignored here; extra
		// list elements beyond a struct's fields are simply left unread by
		// Unmarshal.
		var authResponse struct {
			Mechanism string
		}
		if err := sc.Read(&authResponse); err != nil {
			done <- err
			return
		}
		if authResponse.Mechanism != "ANONYMOUS" {
			done <- fmt.Errorf("client sent mechanism %q, want ANONYMOUS", authResponse.Mechanism)
			return
		}
		if err := sc.WriteSuccess([]any{}); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	if err := c.handleAuth(); err != nil {
		t.Fatalf("handleAuth: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

// TestNewClient drives a full greeting/auth/repos-info handshake over an
// in-memory connection, the way [Server.Serve] itself would answer it, and
// checks NewClient completes it and fills in c.Info.
func TestNewClient(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()

	done := make(chan error, 1)
	go func() {
		var server Server
		server.GetLatestRev = func() (int, error) { return 0, nil }
		done <- server.Serve(serverSide, serverSide)
	}()

	c, err := NewClient(clientSide, clientSide, "svn+ssh://example.com/repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Info.URL != "svn+ssh://example.com/repo" {
		t.Errorf("c.Info.URL = %q, want %q", c.Info.URL, "svn+ssh://example.com/repo")
	}
	if c.Info.UUID == "" {
		t.Errorf("c.Info.UUID is empty")
	}

	clientSide.Close()
	<-done
}

// TestClientConcurrentUse drives many goroutines through the same Client
// at once, calling two different RPCs, against a real Server on the other
// end of an in-memory connection. Without Client's internal locking, this
// reliably corrupts the wire (interleaved writes, reads landing on the
// wrong response) and either errors or hangs; run with -race, it also
// catches the underlying data race on conn's internal Itemizer directly.
func TestClientConcurrentUse(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()

	var server Server
	server.GetLatestRev = func() (int, error) { return 42, nil }
	server.Stat = func(path string, rev *uint) (Dirent, error) {
		return Dirent{Kind: "file", CreatedRev: 1}, nil
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serverSide, serverSide) }()

	c, err := NewClient(clientSide, clientSide, "svn+ssh://example.com/repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	const n = 30
	errs := make(chan error, 2*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if rev, err := c.GetLatestRev(); err != nil {
				errs <- fmt.Errorf("GetLatestRev: %w", err)
			} else if rev != 42 {
				errs <- fmt.Errorf("GetLatestRev() = %d, want 42", rev)
			}
		}()
		go func() {
			defer wg.Done()
			if stat, err := c.Stat("", nil); err != nil {
				errs <- fmt.Errorf("Stat: %w", err)
			} else if stat.Kind != "file" {
				errs <- fmt.Errorf("Stat().Kind = %q, want %q", stat.Kind, "file")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	clientSide.Close()
	if err := <-serveErr; err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}
}
