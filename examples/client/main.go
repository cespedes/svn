package main

import (
	"log"
	"os"

	"github.com/cespedes/svn"
)

func main() {
	c, err := svn.Connect(os.Args[1])

	if err != nil {
		log.Fatal(err)
	}
	log.Printf("# client is %v\n", c)
}
