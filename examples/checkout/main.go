package main

import (
	"fmt"
	"log"
	"os"

	"github.com/cespedes/svn"
)

// This demonstrates Client.Checkout and svn.Editor without touching a
// filesystem at all: it just prints what a real checkout would create.
// See cmd/go-svn's own "checkout" subcommand for a complete,
// filesystem-backed Editor built the same way.
func main() {
	c, err := svn.Connect(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	editor := svn.Editor{
		AddDir: func(path string, copyFrom *svn.EditorCopyFrom) error {
			fmt.Printf("A  %s/\n", path)
			return nil
		},
		AddFile: func(path string, copyFrom *svn.EditorCopyFrom) error {
			fmt.Printf("A  %s\n", path)
			return nil
		},
		CloseFile: func(path string, content []byte) error {
			fmt.Printf("   (%d bytes)\n", len(content))
			return nil
		},
	}

	rev, err := c.Checkout(nil, editor) // nil: check out the latest revision
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Checked out revision %d.\n", rev)
}
