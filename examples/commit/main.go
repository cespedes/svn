package main

import (
	"fmt"
	"io"
	"log"
	"net"

	"github.com/cespedes/svn"
)

// This runs both sides of a commit in one process, over an in-memory
// connection: a svn.Server whose Commit callback just prints every node
// the client describes (a real one would apply them to some backing
// store), and a svn.Client sending a commit built with svn.EditorWriter.
func main() {
	clientSide, serverSide := net.Pipe()

	var server svn.Server
	server.Commit = func(logMessage string, revprops []svn.PropList) (svn.Editor, error) {
		fmt.Printf("commit: %q\n", logMessage)
		return svn.Editor{
			AddDir: func(path string, copyFrom *svn.EditorCopyFrom) error {
				fmt.Printf("  A  %s/\n", path)
				return nil
			},
			AddFile: func(path string, copyFrom *svn.EditorCopyFrom) error {
				fmt.Printf("  A  %s\n", path)
				return nil
			},
			CloseFile: func(path string, content []byte) error {
				fmt.Printf("     %s: %q\n", path, content)
				return nil
			},
			DeleteEntry: func(path string, rev *int) error {
				fmt.Printf("  D  %s\n", path)
				return nil
			},
		}, nil
	}
	server.FinishCommit = func() (svn.CommitInfo, error) {
		return svn.CommitInfo{Rev: 1, Date: "2024-01-01T00:00:00.000000Z", Author: "example"}, nil
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serverSide, serverSide) }()

	c, err := svn.NewClient(clientSide, clientSide, "svn+ssh://example.com/repo")
	if err != nil {
		log.Fatal(err)
	}

	// Describe the changes: a new trunk/ directory holding one new file,
	// and the removal of an old one.
	e := svn.NewEditorWriter()
	steps := []error{
		e.OpenRoot(nil),
		e.AddDir("trunk", nil),
		e.AddFile("trunk/main.go", nil),
		e.ApplyTextdelta([]byte("package main\n"), nil),
		e.CloseFile(nil),
		e.CloseDir(),
		e.DeleteEntry("old.txt", nil),
		e.CloseDir(),
	}
	for _, err := range steps {
		if err != nil {
			log.Fatal(err)
		}
	}
	items, err := e.Items()
	if err != nil {
		log.Fatal(err)
	}

	info, err := c.Commit("add trunk/main.go", items)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("committed revision %d by %s\n", info.Rev, info.Author)

	clientSide.Close()
	if err := <-serveErr; err != io.EOF {
		log.Fatal(err)
	}
}
