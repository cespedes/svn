package svn

import (
	"net"
	"testing"
)

// TestServerUpdateAndSetPathInvokeCallbacks checks that Server.Update and
// Server.SetPath are actually called with the parsed command arguments.
// They used to be checked for nil (to decide whether to reply
// "unimplemented") but never invoked.
func TestServerUpdateAndSetPathInvokeCallbacks(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var gotUpdateRev *uint
	var gotUpdateTarget string
	var gotUpdateRecurse bool
	updateCalled := false

	var gotSetPathPath string
	var gotSetPathRev uint
	var gotSetPathStartEmpty bool
	setPathCalled := false

	var server Server
	server.Update = func(rev *uint, target string, recurse bool) {
		updateCalled = true
		gotUpdateRev = rev
		gotUpdateTarget = target
		gotUpdateRecurse = recurse
	}
	server.SetPath = func(path string, rev uint, startEmpty bool) {
		setPathCalled = true
		gotSetPathPath = path
		gotSetPathRev = rev
		gotSetPathStartEmpty = startEmpty
	}
	server.GetLatestRev = func() (int, error) {
		return 99, nil
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(serverSide, serverSide)
	}()

	cc := conn{r: clientSide, w: clientSide}

	// Greeting: read the server's, send ours.
	var item Item
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading greeting: %v", err)
	}
	if err := cc.Write([]any{2, []any{}, []byte("svn://example.com/repo"), []byte("test-client"), []any{}}); err != nil {
		t.Fatalf("sending greeting response: %v", err)
	}

	// Auth: read the auth-request, send an auth-response, read the ack and
	// the repos-info that follows it.
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading auth-request: %v", err)
	}
	if err := cc.Write([]any{"ANONYMOUS", []any{[]byte{}}}); err != nil {
		t.Fatalf("sending auth-response: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading auth ack: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading repos-info: %v", err)
	}

	// "update": has a direct response, which we read to know Update() has
	// already run by the time it arrives.
	rev := uint(42)
	if err := cc.Write([]any{"update", []any{
		[]uint{rev},
		[]byte("trunk"),
		true,
	}}); err != nil {
		t.Fatalf("sending update: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading update response: %v", err)
	}

	if !updateCalled {
		t.Errorf("Update was not called")
	}
	if gotUpdateRev == nil || *gotUpdateRev != rev {
		t.Errorf("Update rev = %v, want %d", gotUpdateRev, rev)
	}
	if gotUpdateTarget != "trunk" {
		t.Errorf("Update target = %q, want %q", gotUpdateTarget, "trunk")
	}
	if !gotUpdateRecurse {
		t.Errorf("Update recurse = false, want true")
	}

	// "set-path": has no response of its own, so follow it with a
	// "get-latest-rev" (which does) to know set-path was fully processed
	// -- Serve handles one command at a time, sequentially.
	if err := cc.Write([]any{"set-path", []any{
		[]byte("trunk/foo"),
		uint(7),
		false,
	}}); err != nil {
		t.Fatalf("sending set-path: %v", err)
	}
	if err := cc.Write([]any{"get-latest-rev", []any{}}); err != nil {
		t.Fatalf("sending get-latest-rev: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading get-latest-rev auth ack: %v", err)
	}
	var rec struct{ Rev int }
	if err := cc.ReadResponse(&rec); err != nil {
		t.Fatalf("reading get-latest-rev response: %v", err)
	}
	if rec.Rev != 99 {
		t.Errorf("get-latest-rev = %d, want 99", rec.Rev)
	}

	if !setPathCalled {
		t.Errorf("SetPath was not called")
	}
	if gotSetPathPath != "trunk/foo" {
		t.Errorf("SetPath path = %q, want %q", gotSetPathPath, "trunk/foo")
	}
	if gotSetPathRev != 7 {
		t.Errorf("SetPath rev = %d, want 7", gotSetPathRev)
	}
	if gotSetPathStartEmpty {
		t.Errorf("SetPath startEmpty = true, want false")
	}

	clientSide.Close()
	<-serveErr
}
