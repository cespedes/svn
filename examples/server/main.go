package main

import (
	"log"
	"os"

	"github.com/cespedes/svn"
)

func main() {
	var server svn.Server
	lastRev := 1000
	server.GetLatestRev = func() (int, error) {
		return lastRev, nil
	}
	server.Stat = func(path string, rev *uint) (svn.Dirent, error) {
		return svn.Dirent{
			Kind:        "dir",
			CreatedDate: "2024-03-18T14:50:07.758412Z",
		}, nil
	}
	server.CheckPath = func(path string, rev *uint) (string, error) {
		return "dir", nil
	}
	err := server.Serve(os.Stdin, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}
}
