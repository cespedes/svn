package main

import (
	"log"
	"os"

	"github.com/cespedes/svn"
)

func main() {
	c, err := svn.NewClient(os.Stdin, os.Stdout)

	if err != nil {
		log.Fatal(err)
	}
	log.Printf("# client is %v\n", c)
}
