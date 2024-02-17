package main

import (
	"log"
	"os"

	"github.com/cespedes/svn"
)

func callback(item svn.Item, conn svn.Conn) error {
	log.Printf("item: %v\n", item)
	return nil
}

func main() {
	err := svn.Serve(os.Stdin, os.Stdout, callback)
	if err != nil {
		log.Fatal(err)
	}
}
