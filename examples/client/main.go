package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/cespedes/svn"
)

func main() {
	c, err := svn.Connect(os.Args[1])

	if err != nil {
		log.Fatal(err)
	}

	rev, err := c.GetLatestRev()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Last revision: %d\n", rev)

	stat, err := c.Stat("", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Stat: %+v\n", stat)

	dirents, err := c.List("", nil, "immediates", []string{"kind", "size", "created-rev", "time", "last-author"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("List: %+v\n", dirents)

	// GetFile needs a *file*, not a directory: if the given URL is a
	// directory (the common case), find one of its own children to
	// demonstrate GetFile on, rather than always querying "" (the
	// connected path itself, which would fail with a low-level
	// svnserve error -- "Attempted to get checksum of a *non*-file
	// node" -- if it's a directory). List always includes the queried
	// directory itself as one of its own "children" (see Server.List's
	// doc comment); it's the one entry whose Path is the shortest,
	// since every other child's Path is exactly that plus "/" plus its
	// own name.
	target := ""
	if stat.Kind == "dir" {
		selfPath := dirents[0].Path
		for _, d := range dirents {
			if len(d.Path) < len(selfPath) {
				selfPath = d.Path
			}
		}
		for _, d := range dirents {
			if d.Path != selfPath && d.Kind == "file" {
				target = strings.TrimPrefix(strings.TrimPrefix(d.Path, selfPath), "/")
				break
			}
		}
		if target == "" {
			fmt.Println("(no file found directly under this directory; skipping GetFile)")
			return
		}
	}

	props, content, err := c.GetFile(target, nil, true, true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("GetFile (proplist): %+v\n", props)
	fmt.Printf("GetFile (content): %q\n", content)
}
