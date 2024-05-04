package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/cespedes/svn"
)

func main() {
	err := run(os.Args, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err.Error())
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	var err error
	var revStr string
	var rev1, rev2 int
	var lrev1, lrev2 *int
	var verbose bool
	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.BoolVar(&verbose, "v", false, "verbose")
	f.StringVar(&revStr, "r", "", "revision (rev or rev1:rev2")
	f.Parse(args[1:])

	if revStr != "" {
		srev1, srev2, found := strings.Cut(revStr, ":")
		rev1, err = strconv.Atoi(srev1)
		if err != nil {
			return fmt.Errorf("error parsing -r argument: %w", err)
		}
		lrev1 = &rev1
		if found {
			rev2, err = strconv.Atoi(srev2)
			if err != nil {
				return fmt.Errorf("error parsing -r argument: %w", err)
			}
			lrev2 = &rev2
		}
	}

	args = f.Args()
	if len(args) == 1 && args[0] == "help" {
		help(stdout)
		return nil
	}
	if len(args) != 2 {
		return fmt.Errorf("type 'go-svn help' for usage")
	}
	switch args[0] {
	case "info":
		if verbose {
			return errors.New("subcommand 'info' does not accept option '-v'")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'info' does not accept revision range")
		}
		return svnInfo(args[1], lrev1, stdout)
	case "cat":
		if verbose {
			return errors.New("subcommand 'info' does not accept option '-v'")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'info' does not accept revision range")
		}
		return svnCat(args[1], lrev1, stdout)
	case "ls":
		if lrev2 != nil {
			return errors.New("subcommand 'ls' does not accept revision range")
		}
		return svnLs(args[1], lrev1, verbose, stdout)
	case "log":
		return svnLog(args[1], lrev1, lrev2, verbose, stdout)
	default:
		return fmt.Errorf(`unknown subcommand: '%s'
Type 'svn help' for usage`, args[0])
	}
	return nil
}

func svnInfo(repo string, lrev *int, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	rev, err := c.GetLatestRev()
	if err != nil {
		log.Fatal(err)
	}

	stat, err := c.Stat("", nil)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "URL: %s\n", repo)
	// fmt.Fprintf(stdout, "Relative URL: %s\n", XXX)
	fmt.Fprintf(stdout, "Repository Root: %s\n", c.Info.URL)
	fmt.Fprintf(stdout, "Repository UUID: %s\n", c.Info.UUID)
	fmt.Fprintf(stdout, "Revision: %d\n", rev)
	fmt.Fprintf(stdout, "Node Kind: %s\n", stat.Kind)
	fmt.Fprintf(stdout, "Last Changed Author: %s\n", stat.LastAuthor)
	fmt.Fprintf(stdout, "Last Changed Rev: %d\n", stat.CreatedRev)
	fmt.Fprintf(stdout, "Last Changed Date: %s\n", stat.CreatedDate)

	return nil
}

func svnCat(repo string, lrev *int, stdout io.Writer) error {
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

func svnLs(repo string, lrev *int, verbose bool, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	dirents, err := c.List("", lrev, "immediates", []string{"kind", "size", "created-rev", "time", "last-author"})
	if err != nil {
		return err
	}
	maxAuthorLen := 8
	maxRevLen := 5
	maxSizeLen := 6
	for _, entry := range dirents {
		if len(entry.LastAuthor) > maxAuthorLen {
			maxAuthorLen = len(entry.LastAuthor)
		}
		if len(fmt.Sprint(entry.CreatedRev)) > maxRevLen {
			maxRevLen = len(fmt.Sprint(entry.CreatedRev))
		}
		if entry.Kind == "file" && (len(fmt.Sprint(entry.Size)) > maxSizeLen) {
			maxSizeLen = len(fmt.Sprint(entry.Size))
		}
	}
	ur, err := url.Parse(c.Info.URL)
	if err != nil {
		return fmt.Errorf("parsing repo URL: %w", err) // should not happen
	}
	if len(repo) > 0 && repo[len(repo)-1] == '/' {
		repo = repo[0 : len(repo)-1]
	}
	ua, err := url.Parse(repo)
	if err != nil {
		return fmt.Errorf("parsing URL: %w", err) // should not happen
	}
	// fmt.Printf("repo url path: %s\n", ur.Path)
	// fmt.Printf("asked path: %s\n", ua.Path)
	for _, entry := range dirents {
		p := entry.Path
		localpart := ""
		if strings.HasPrefix(ua.Path, ur.Path) {
			localpart = ua.Path[len(ur.Path):]
		}
		// fmt.Printf("local part: %s\n", localpart)
		p = strings.TrimPrefix(p, localpart)
		if len(p) > 0 && p[0] == '/' {
			p = p[1:]
		}
		if p == "" {
			p = "."
		}
		size := fmt.Sprint(entry.Size)
		if entry.Kind == "dir" {
			size = ""
			p += "/"
		}
		if verbose {
			date := entry.CreatedDate[0:10] + " " + entry.CreatedDate[11:19]
			fmt.Fprintf(stdout, "%*d %-*s %*s %s %s\n",
				maxRevLen, entry.CreatedRev,
				maxAuthorLen, entry.LastAuthor,
				maxSizeLen, size,
				date, p)
		} else {
			if p != "./" {
				fmt.Fprintf(stdout, "%s\n", p)
			}
		}
	}

	return nil
}

func svnLog(repo string, lrev1 *int, lrev2 *int, verbose bool, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	logs, err := c.Log(nil, lrev1, lrev2, verbose)
	if err != nil {
		log.Fatal(err)
	}

	for _, l := range logs {
		if l.Rev == 0 {
			break
		}
		fmt.Fprintln(stdout, "------------------------------------------------------------------------")
		slines := "1 line"
		lines := strings.Count(l.Message, "\n")
		if lines > 0 {
			slines = fmt.Sprintf("%d lines", lines+1)
		}
		fmt.Fprintf(stdout, "r%d | %s | %s | %s\n", l.Rev, l.Author, l.Date, slines)
		if len(l.Changed) > 0 {
			fmt.Fprintln(stdout, "Changed paths:")
			for _, c := range l.Changed {
				fmt.Fprintf(stdout, "%4s %s\n", c.Mode, c.Path)
			}
		}
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, l.Message)
	}
	fmt.Fprintln(stdout, "------------------------------------------------------------------------")

	return nil
}

func help(stdout io.Writer) {
	fmt.Fprintln(stdout, `usage: go-svn [-v] [-r revision[:revision2]] <subcommand> <repo>

Available subcommands:
   info
   cat
   ls
   log

go-svn is a client for the Subversion protocol.`)
}
