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
}
