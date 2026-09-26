package svn

import (
	"errors"
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

// TestServerFinishReportReceivesReportedPaths checks that Serve accumulates
// every "set-path" command into a []ReportedPath and passes it to
// Server.FinishReport, in the order the client sent them -- and that this
// accumulation happens even though Server.SetPath itself is left nil here,
// since it is meant to be purely an optional, informational callback.
func TestServerFinishReportReceivesReportedPaths(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var gotReport []ReportedPath
	var server Server
	server.FinishReport = func(report []ReportedPath) ([]Item, error) {
		gotReport = report
		return nil, nil
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

	// Two "set-path" commands, as a real checkout sends for the report
	// root (start-empty, since it has nothing) plus one already-present
	// subdirectory, then "finish-report" to end the exchange.
	if err := cc.Write([]any{"set-path", []any{
		[]byte(""),
		uint(5),
		true,
	}}); err != nil {
		t.Fatalf("sending first set-path: %v", err)
	}
	if err := cc.Write([]any{"set-path", []any{
		[]byte("sub"),
		uint(5),
		false,
	}}); err != nil {
		t.Fatalf("sending second set-path: %v", err)
	}
	if err := cc.Write([]any{"finish-report", []any{}}); err != nil {
		t.Fatalf("sending finish-report: %v", err)
	}

	// finish-report's own two-part response: an immediate empty
	// auth-request-shaped ack, then (since FinishReport returned no
	// items and no error) "close-edit" straight away.
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading finish-report ack: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading close-edit: %v", err)
	}
	if item.Type != ListType || len(item.List) != 2 || item.List[0].Text != "close-edit" {
		t.Fatalf("got %s, want a close-edit command", item)
	}
	// Serve waits for a client response to close-edit before it can move
	// on to whatever command comes next (there is none here, but Serve
	// doesn't know that).
	if err := cc.Write([]any{"success", []any{}}); err != nil {
		t.Fatalf("acking close-edit: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading finish-report's final response: %v", err)
	}

	want := []ReportedPath{
		{Path: "", Rev: 5, StartEmpty: true},
		{Path: "sub", Rev: 5, StartEmpty: false},
	}
	if len(gotReport) != len(want) {
		t.Fatalf("FinishReport got %+v, want %+v", gotReport, want)
	}
	for i := range want {
		if gotReport[i] != want[i] {
			t.Errorf("FinishReport report[%d] = %+v, want %+v", i, gotReport[i], want[i])
		}
	}

	clientSide.Close()
	<-serveErr
}

// TestServerFinishReportErrorReadsAbortEditAck checks that, when
// FinishReport returns an error, Serve reads the client's ack of the
// resulting "abort-edit" (the same way it already does for a successful
// "close-edit") before answering "finish-report" itself -- and that the
// connection is still usable afterward. Leaving that ack unread desyncs
// the connection: the next command a real client sends is instead read
// as that stale ack, misinterpreted as a bogus command (confirmed
// against a real svn client, which then fails with "Unknown command
// 'success'"; see CLAUDE.md).
func TestServerFinishReportErrorReadsAbortEditAck(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var server Server
	server.FinishReport = func(report []ReportedPath) ([]Item, error) {
		return nil, errors.New("unsupported report shape")
	}
	server.GetLatestRev = func() (int, error) { return 42, nil }

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(serverSide, serverSide)
	}()

	cc := conn{r: clientSide, w: clientSide}

	var item Item
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading greeting: %v", err)
	}
	if err := cc.Write([]any{2, []any{}, []byte("svn://example.com/repo"), []byte("test-client"), []any{}}); err != nil {
		t.Fatalf("sending greeting response: %v", err)
	}
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

	if err := cc.Write([]any{"set-path", []any{
		[]byte(""), uint(5), true,
	}}); err != nil {
		t.Fatalf("sending set-path: %v", err)
	}
	if err := cc.Write([]any{"finish-report", []any{}}); err != nil {
		t.Fatalf("sending finish-report: %v", err)
	}

	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading finish-report ack: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading abort-edit: %v", err)
	}
	if item.Type != ListType || len(item.List) != 2 || item.List[0].Text != "abort-edit" {
		t.Fatalf("got %s, want an abort-edit command", item)
	}
	if err := cc.Write([]any{"success", []any{}}); err != nil {
		t.Fatalf("acking abort-edit: %v", err)
	}
	// This must be finish-report's own "(success ())" response. If Serve
	// never read the ack above, it's still sitting unread in the pipe,
	// and Serve's next read (of a *command*, not a response) consumes it
	// instead -- misread as a bogus command named "success", which
	// Serve replies to with a failure response; that failure is what
	// ReadResponse would see here instead, and return as an error.
	if err := cc.ReadResponse(&struct{}{}); err != nil {
		t.Fatalf("reading finish-report's final response: %v", err)
	}

	// A further command still gets a normal response: the desync above
	// is exactly one exchange wide (Serve's own failure reply to the
	// misread "success" resyncs the connection), so this alone wouldn't
	// catch a regression -- the ReadResponse call above is what does.
	if err := cc.Write([]any{"get-latest-rev", []any{}}); err != nil {
		t.Fatalf("sending get-latest-rev: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading get-latest-rev ack: %v", err)
	}
	var rec struct{ Rev int }
	if err := cc.ReadResponse(&rec); err != nil {
		t.Fatalf("reading get-latest-rev response: %v", err)
	}
	if rec.Rev != 42 {
		t.Errorf("get-latest-rev = %d, want 42", rec.Rev)
	}

	clientSide.Close()
	<-serveErr
}

// TestServerGetIProps checks that "get-iprops" gets a real answer instead
// of "Unknown command" (there is no Server callback field for it at all:
// inherited properties aren't modeled anywhere in this package, so it
// always reports none -- see the "get-iprops" case's own comment in
// server.go), and that the response is shaped the same two-write way as
// every other command here ("empty auth-request" pre-ack, then the real
// answer): sending only one write for it (as an earlier version did)
// leaves a real client waiting forever for the second, since nothing
// else in the exchange would otherwise prompt Serve to send it.
func TestServerGetIProps(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var server Server

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(serverSide, serverSide)
	}()

	cc := conn{r: clientSide, w: clientSide}

	var item Item
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading greeting: %v", err)
	}
	if err := cc.Write([]any{2, []any{}, []byte("svn://example.com/repo"), []byte("test-client"), []any{}}); err != nil {
		t.Fatalf("sending greeting response: %v", err)
	}
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

	if err := cc.Write([]any{"get-iprops", []any{[]byte(""), []any{}}}); err != nil {
		t.Fatalf("sending get-iprops: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading get-iprops pre-ack: %v", err)
	}
	var iprops struct{ Inherited []Item }
	if err := cc.ReadResponse(&iprops); err != nil {
		t.Fatalf("reading get-iprops response: %v", err)
	}
	if len(iprops.Inherited) != 0 {
		t.Errorf("Inherited = %+v, want none", iprops.Inherited)
	}

	clientSide.Close()
	<-serveErr
}
