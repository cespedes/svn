package svn

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

// TestServerDebug checks that Server.Debug logs every Item Serve reads
// or writes, prefixed "> "/"< ", including the greeting itself (unlike
// Client.SetDebug, Serve has no already-handshaken connection handed to
// it -- it writes the greeting itself, so Debug being set from the start
// sees all of it).
func TestServerDebug(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var buf bytes.Buffer
	var server Server
	server.Debug = &buf
	server.GetLatestRev = func() (int, error) { return 7, nil }

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serverSide, serverSide) }()

	c, err := NewClient(clientSide, clientSide, "svn+ssh://example.com/repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.GetLatestRev(); err != nil {
		t.Fatalf("GetLatestRev: %v", err)
	}
	clientSide.Close()
	if err := <-serveErr; err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "> ( success") {
		t.Errorf("debug log missing the server's own greeting:\n%s", logged)
	}
	if !strings.Contains(logged, "< ( get-latest-rev") {
		t.Errorf("debug log missing the received command:\n%s", logged)
	}
	if !strings.Contains(logged, "> ( success ( 7 ) )") {
		t.Errorf("debug log missing the sent response:\n%s", logged)
	}
}

// TestServerUpdateAndSetPathInvokeCallbacks checks that Server.Update and
// Server.SetPath are actually called with the parsed command arguments.
// They used to be checked for nil (to decide whether to reply
// "unimplemented") but never invoked.
// TestServerCommit drives a full round trip through this package's own
// Client and Server: Client.Commit sends an Editor Command Set built
// with EditorWriter (adding a directory and a file, then deleting an
// unrelated existing one), and Server.Commit/FinishCommit -- backed by
// a small in-memory tree, standing in for whatever a real backing store
// would do -- receive it via the same driveEditor reader
// Client.Checkout/Update/Diff already use to parse a server-driven
// edit, confirming it works the same regardless of direction.
func TestServerCommit(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	type node struct {
		isDir   bool
		content []byte
	}
	tree := map[string]*node{
		"":              {isDir: true},
		"trunk/old.txt": {content: []byte("stale\n")},
	}
	var gotLogMessage string
	var gotRevprops []PropList

	var server Server
	server.Commit = func(logMessage string, revprops []PropList) (Editor, error) {
		gotLogMessage = logMessage
		gotRevprops = revprops
		editor := Editor{
			AddDir: func(path string, copyFrom *EditorCopyFrom) error {
				tree[path] = &node{isDir: true}
				return nil
			},
			AddFile: func(path string, copyFrom *EditorCopyFrom) error {
				tree[path] = &node{}
				return nil
			},
			CloseFile: func(path string, content []byte) error {
				tree[path].content = content
				return nil
			},
			DeleteEntry: func(path string, rev *int) error {
				delete(tree, path)
				return nil
			},
		}
		return editor, nil
	}
	server.FinishCommit = func() (CommitInfo, error) {
		return CommitInfo{Rev: 42, Date: "2024-01-01T00:00:00.000000Z", Author: "tester"}, nil
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serverSide, serverSide) }()

	c, err := NewClient(clientSide, clientSide, "svn+ssh://example.com/repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	e := NewEditorWriter()
	if err := e.OpenRoot(nil); err != nil {
		t.Fatal(err)
	}
	if err := e.AddDir("trunk", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFile("trunk/main.go", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyTextdelta([]byte("package main\n"), nil); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseFile(nil); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteEntry("trunk/old.txt", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseDir(); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseDir(); err != nil {
		t.Fatal(err)
	}
	items, err := e.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}

	info, err := c.Commit("test commit", items)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if info.Rev != 42 || info.Author != "tester" || info.Date != "2024-01-01T00:00:00.000000Z" {
		t.Errorf("Commit() = %+v, want Rev=42 Author=tester Date=2024-01-01T00:00:00.000000Z", info)
	}

	if gotLogMessage != "test commit" {
		t.Errorf("Server.Commit's own logMessage = %q, want %q", gotLogMessage, "test commit")
	}
	foundLog := false
	for _, p := range gotRevprops {
		if p.Name == "svn:log" {
			foundLog = true
			if p.Value != "test commit" {
				t.Errorf("svn:log revprop = %q, want %q", p.Value, "test commit")
			}
		}
	}
	if !foundLog {
		t.Errorf("revprops missing svn:log: %+v", gotRevprops)
	}

	if tree["trunk"] == nil || !tree["trunk"].isDir {
		t.Errorf("trunk not recorded as a directory: %+v", tree["trunk"])
	}
	if got := tree["trunk/main.go"]; got == nil || string(got.content) != "package main\n" {
		t.Errorf("trunk/main.go = %+v, want content %q", got, "package main\n")
	}
	if _, ok := tree["trunk/old.txt"]; ok {
		t.Errorf("trunk/old.txt should have been deleted")
	}

	clientSide.Close()
	if err := <-serveErr; err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}
}

// TestServerCommitUnimplemented checks that "commit" replies
// "unimplemented" (rather than, say, panicking on a nil Editor) when
// Server.Commit/FinishCommit aren't set, the same way every other
// nil-able callback field does.
func TestServerCommitUnimplemented(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var server Server
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serverSide, serverSide) }()

	c, err := NewClient(clientSide, clientSide, "svn+ssh://example.com/repo")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	e := NewEditorWriter()
	if err := e.OpenRoot(nil); err != nil {
		t.Fatal(err)
	}
	if err := e.CloseDir(); err != nil {
		t.Fatal(err)
	}
	items, err := e.Items()
	if err != nil {
		t.Fatalf("Items: %v", err)
	}

	if _, err := c.Commit("test commit", items); err == nil {
		t.Fatalf("Commit succeeded against a Server with no Commit callback, want an error")
	}

	clientSide.Close()
	<-serveErr
}

// TestServerCommitAbortEdit checks the defensive, not confirmed against
// a real client, path: if the client sends "abort-edit" instead of
// "close-edit", Serve acks it the same way (an empty success, exactly
// like driveEditor already does for the read direction) and reports the
// "commit" command's own deferred response as a failure, rather than
// hanging or calling FinishCommit as if the edit had completed
// successfully.
func TestServerCommitAbortEdit(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	finishCommitCalled := false
	var server Server
	server.Commit = func(logMessage string, revprops []PropList) (Editor, error) {
		return Editor{}, nil
	}
	server.FinishCommit = func() (CommitInfo, error) {
		finishCommitCalled = true
		return CommitInfo{Rev: 1}, nil
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serverSide, serverSide) }()

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

	if err := cc.Write([]any{"commit", []any{[]byte("msg"), []any{}, false, []any{}}}); err != nil {
		t.Fatalf("sending commit: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading commit pre-ack: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading commit ack: %v", err)
	}

	if err := cc.Write([]any{"open-root", []any{[]any{}, []byte("d0")}}); err != nil {
		t.Fatalf("sending open-root: %v", err)
	}
	if err := cc.Write([]any{"abort-edit", []any{}}); err != nil {
		t.Fatalf("sending abort-edit: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading abort-edit ack: %v", err)
	}
	if item.Type != ListType || len(item.List) < 1 || item.List[0].Text != "success" {
		t.Fatalf("abort-edit ack = %s, want a success", item)
	}

	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading commit's own deferred pre-ack: %v", err)
	}
	if err := cc.ReadResponse(&struct{}{}); err == nil {
		t.Errorf("commit's own deferred response succeeded after abort-edit, want a failure")
	}
	if finishCommitCalled {
		t.Errorf("FinishCommit should not be called after the client aborted the edit")
	}

	clientSide.Close()
	<-serveErr
}

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
// 'success'").
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

// TestServerDiffAcksImmediately checks that "diff" gets its own "empty
// auth-request" ack right away, the same way "update" does -- confirmed
// by raw wire capture, reading immediately after sending "diff" and
// before sending "set-path" at all. A first version of this fix assumed
// the ack was instead deferred until "finish-report" (from an earlier,
// less careful capture that sent "set-path" and "finish-report" before
// reading anything, which couldn't actually distinguish "sent
// immediately but read late" from "sent late"); a real "svn diff" client
// left waiting forever for the immediate ack that assumption never sent
// is what caught the mistake.
func TestServerDiffAcksImmediately(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var gotDiff bool
	var server Server
	server.Diff = func(rev *uint, target string, recurse, ignoreAncestry bool, versusURL string, textDeltas bool, depth string) {
		gotDiff = true
	}
	server.FinishReport = func(report []ReportedPath) ([]Item, error) {
		return nil, nil
	}

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

	two := uint(2)
	if err := cc.Write([]any{"diff", []any{
		[]any{two}, []byte(""), true, false, []byte("svn://example.com/repo"), true, "infinity",
	}}); err != nil {
		t.Fatalf("sending diff: %v", err)
	}
	// Read before sending set-path at all, so this can only pass if the
	// ack genuinely arrives right away.
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading diff's immediate ack: %v", err)
	}
	if !gotDiff {
		t.Errorf("Diff was not called")
	}

	if err := cc.Write([]any{"set-path", []any{[]byte(""), uint(1), false}}); err != nil {
		t.Fatalf("sending set-path: %v", err)
	}
	if err := cc.Write([]any{"finish-report", []any{}}); err != nil {
		t.Fatalf("sending finish-report: %v", err)
	}

	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading finish-report's own ack: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading close-edit: %v", err)
	}
	if item.Type != ListType || len(item.List) != 2 || item.List[0].Text != "close-edit" {
		t.Fatalf("got %s, want a close-edit command", item)
	}
	if err := cc.Write([]any{"success", []any{}}); err != nil {
		t.Fatalf("acking close-edit: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading finish-report's final response: %v", err)
	}

	clientSide.Close()
	<-serveErr
}

// TestServerGetDir checks "get-dir" (the older, pre-"list" way to read a
// directory's children, which "svn diff" still falls back to for
// enumerating a deleted directory's former contents) against golden
// text captured from a real svnserve: the response is
// "( rev:number props:proplist ( dirent ... ) )", and each dirent is
// "( name:string kind:word size:number has-props:bool created-rev:number
// [ created-date:string ] [ last-author:string ] )" -- confirmed to have
// every field present as a bare value except the trailing two, unlike
// "list"'s own, fully-optional field set.
func TestServerGetDir(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	var server Server
	server.List = func(path string, rev *uint, depth string, fields, pattern []string) ([]Dirent, error) {
		if path != "trunk" {
			t.Errorf("List called with path %q, want %q", path, "trunk")
		}
		return []Dirent{
			{Path: "/trunk", Kind: "dir", CreatedRev: 3},
			{
				Path: "/trunk/main.go", Kind: "file", Size: 13, HasProps: false,
				CreatedRev: 2, CreatedDate: "2024-01-01T00:00:00.000000Z", LastAuthor: "tester",
			},
		}, nil
	}

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

	if err := cc.Write([]any{"get-dir", []any{
		[]byte("trunk"), []any{uint(2)}, true, true, []any{"kind"}, false,
	}}); err != nil {
		t.Fatalf("sending get-dir: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading get-dir pre-ack: %v", err)
	}
	if err := cc.Read(&item); err != nil {
		t.Fatalf("reading get-dir response: %v", err)
	}

	want := `( success ( 3 ( ) ( ( 7:main.go file 13 false 2 ( 27:2024-01-01T00:00:00.000000Z ) ( 6:tester ) ) ) ) )`
	if item.String() != want {
		t.Errorf("get-dir response =\n%s\nwant:\n%s", item, want)
	}

	clientSide.Close()
	<-serveErr
}
