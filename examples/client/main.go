package main

import (
	"fmt"
	"log"
	"os"

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

	rev = 1
	dirents, err := c.List("", &rev, "immediates", []string{"kind", "size", "created-rev", "time", "last-author"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("List: %+v\n", dirents)
}
