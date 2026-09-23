package svn

import (
	"net"
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
		// "stat" case: a list with one element, which is the dirent tuple.
		if err := sc.WriteSuccess([]any{[]any{
			"file", uint64(1234), true, uint(42),
			[]any{[]byte("2024-04-02T13:37:34.350221Z")},
			[]any{[]byte("juan")},
		}}); err != nil {
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
