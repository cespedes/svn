package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/cespedes/svn"
)

func main() {
	err := run(os.Args, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	var rev int
	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.IntVar(&rev, "r", -1, "revision")
	f.Parse(args[1:])
	args = f.Args()
	var lrev *int
	if rev >= 0 {
		lrev = &rev
	}
	if len(args) == 1 && args[0] == "help" {
		help(stdout)
		return nil
	}
	if len(args) != 2 {
		return fmt.Errorf("Type 'go-svn help' for usage.")
	}
	switch args[0] {
	case "info":
		return info(args[1], lrev, stdout)
	case "cat":
		return cat(args[1], lrev, stdout)
	case "ls":
		return ls(args[1], lrev, stdout)
	default:
		return fmt.Errorf(`Unknown subcommand: '%s'
Type 'svn help' for usage.`, args[1])
	}
	return nil
}

func info(repo string, lrev *int, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	rev, err := c.GetLatestRev()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(stdout, "Revision: %d\n", rev)

	stat, err := c.Stat("", nil)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Node Kind: %s\n", stat.Kind)
	fmt.Fprintf(stdout, "Last Changed Author: %s\n", stat.LastAuthor)
	fmt.Fprintf(stdout, "Last Changed Rev: %d\n", stat.CreatedRev)
	fmt.Fprintf(stdout, "Last Changed Date: %s\n", stat.CreatedDate)

	return nil
}

func cat(repo string, lrev *int, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	_, content, err := c.GetFile("", lrev, true, true)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, string(content))

	return nil
}

func ls(repo string, lrev *int, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	dirents, err := c.List("", lrev, "immediates", []string{"kind", "size", "created-rev", "time", "last-author"})
	if err != nil {
		return err
	}
	for _, entry := range dirents {
		path := entry.Path
		// path := entry.Path[1:]
		// if path == "" {
		// 	path = "."
		// }
		size := fmt.Sprint(entry.Size)
		if entry.Kind == "dir" {
			size = ""
			path += "/"
		}
		date := entry.CreatedDate[0:10] + " " + entry.CreatedDate[11:19]
		fmt.Fprintf(stdout, "%7d %-22s %9s %s %s\n", entry.CreatedRev, entry.LastAuthor, size, date, path)
	}

	return nil
}

func help(stdout io.Writer) {
	fmt.Fprintln(stdout, `usage: go-svn [-r revision] <subcommand> <repo>

Available subcommands:
   info
   cat
   ls

go-svn is a client for the Subversion protocol.`)
}
