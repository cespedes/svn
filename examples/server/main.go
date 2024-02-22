package main

import (
	"log"
	"os"

	"github.com/cespedes/svn"
)

func callback(command svn.Command, item svn.Item, conn svn.Conn) error {
	log.Printf("command: %v\n", command)
	return nil
}

func main() {
	err := svn.Serve(os.Stdin, os.Stdout, callback)
	if err != nil {
		log.Fatal(err)
	}
}
