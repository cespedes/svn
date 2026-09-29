package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"

	"github.com/cespedes/svn"
	"github.com/cespedes/svn/svnfs"
)

// This lists every file in the repository (at its latest revision) with
// fs.WalkDir, and prints the size of each one, using svnfs to present
// the repository as a standard io/fs.FS. The same FS could be handed to
// anything else that takes one, e.g. http.FileServerFS.
func main() {
	c, err := svn.Connect(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	fsys := svnfs.New(c, nil) // nil: the latest revision

	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." {
				fmt.Printf("%s/\n", path)
			}
			return nil
		}
		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		fmt.Printf("%s (%d bytes)\n", path, len(content))
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
