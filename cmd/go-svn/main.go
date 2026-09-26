package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
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
	if len(args) < 2 {
		return fmt.Errorf("type 'go-svn help' for usage")
	}
	switch args[0] {
	case "info":
		if len(args) != 2 {
			return errors.New("subcommand 'info' takes exactly one argument (repo URL)")
		}
		if verbose {
			return errors.New("subcommand 'info' does not accept option '-v'")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'info' does not accept revision range")
		}
		return svnInfo(args[1], lrev1, stdout)
	case "cat":
		if len(args) != 2 {
			return errors.New("subcommand 'cat' takes exactly one argument (repo URL)")
		}
		if verbose {
			return errors.New("subcommand 'info' does not accept option '-v'")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'info' does not accept revision range")
		}
		return svnCat(args[1], lrev1, stdout)
	case "ls":
		if len(args) != 2 {
			return errors.New("subcommand 'ls' takes exactly one argument (repo URL)")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'ls' does not accept revision range")
		}
		return svnLs(args[1], lrev1, verbose, stdout)
	case "log":
		if len(args) != 2 {
			return errors.New("subcommand 'log' takes exactly one argument (repo URL)")
		}
		return svnLog(args[1], lrev1, lrev2, verbose, stdout)
	case "export":
		if len(args) > 3 {
			return errors.New("subcommand 'export' takes a repo URL and, optionally, a destination directory")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'export' does not accept revision range")
		}
		dest := ""
		if len(args) == 3 {
			dest = args[2]
		}
		return svnExport(args[1], lrev1, dest, stdout)
	default:
		return fmt.Errorf(`unknown subcommand: '%s'
Type 'svn help' for usage`, args[0])
	}
}

func svnInfo(repo string, lrev *int, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	rev, err := c.GetLatestRev()
	if err != nil {
		return err
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
		return err
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
				if c.Copy != nil {
					fmt.Fprintf(stdout, "%4s %s (from %s:%d)\n", c.Mode, c.Path, c.Copy.Path, c.Copy.Rev)
				} else {
					fmt.Fprintf(stdout, "%4s %s\n", c.Mode, c.Path)
				}
			}
		}
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, l.Message)
	}
	fmt.Fprintln(stdout, "------------------------------------------------------------------------")

	return nil
}

// svnExport writes a clean copy of repo at lrev (nil meaning the latest
// revision) to dest, with no version-control metadata -- the same thing
// "svn export" does. If dest is empty, it defaults to the last path
// segment of repo's URL, matching a real svn client.
func svnExport(repo string, lrev *int, dest string, stdout io.Writer) error {
	c, err := svn.Connect(repo)
	if err != nil {
		return err
	}

	if dest == "" {
		dest, err = defaultExportDest(repo)
		if err != nil {
			return err
		}
	}

	// A real svnserve's "list" response always uses full,
	// repository-root-relative paths (see Server.List's own doc
	// comment), regardless of where the client's own session is
	// anchored -- so if repo points below the repository root (e.g.
	// ".../reponame/trunk"), every Dirent.Path exportDir sees below is
	// prefixed with "trunk", which anchor supplies for matching.
	anchor, err := repoRootRelativePath(c, repo)
	if err != nil {
		return err
	}

	stat, err := c.Stat("", lrev)
	if err != nil {
		return err
	}

	switch stat.Kind {
	case "file":
		if err := exportFile(c, "", lrev, dest); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "A    %s\n", dest)
	case "dir":
		if err := os.Mkdir(dest, 0o755); err != nil {
			return fmt.Errorf("export: %w", err)
		}
		if err := exportDir(c, "", anchor, lrev, dest, stdout); err != nil {
			return err
		}
	default:
		return fmt.Errorf("export: %s: unsupported node kind %q", repo, stat.Kind)
	}

	rev := lrev
	if rev == nil {
		latest, err := c.GetLatestRev()
		if err != nil {
			return err
		}
		rev = &latest
	}
	fmt.Fprintf(stdout, "Exported revision %d.\n", *rev)
	return nil
}

// defaultExportDest derives the local directory name "svn export" uses
// when no destination is given explicitly: the last path segment of
// repo's own URL.
func defaultExportDest(repo string) (string, error) {
	u, err := url.Parse(repo)
	if err != nil {
		return "", fmt.Errorf("export: parsing URL: %w", err)
	}
	name := path.Base(strings.TrimSuffix(u.Path, "/"))
	if name == "" || name == "." || name == "/" {
		return "", errors.New("export: cannot determine a local directory name from the URL; specify one explicitly")
	}
	return name, nil
}

// repoRootRelativePath returns repo's own path relative to the
// repository root Connect discovered (c.Info.URL) -- e.g. "trunk" if
// repo pointed at ".../reponame/trunk". It is "" if repo points at the
// repository root itself.
func repoRootRelativePath(c *svn.Client, repo string) (string, error) {
	rel, err := svn.RepoRelativePath(c.Info.URL, repo)
	if err != nil {
		// Should not happen: Connect resolved repo to this very root.
		return "", fmt.Errorf("export: %w", err)
	}
	return rel, nil
}

// joinNonEmpty joins a and b with "/", except that either being "" just
// yields the other -- unlike path.Join, it never collapses "" into ".".
func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "/" + b
	}
}

// exportDir recursively writes every child of svnPath (at rev) under
// destPath, which must already exist, printing one "A    path" line per
// file or directory written -- the same shape a real "svn export"
// prints. anchor is svnPath's own repository-root-relative form (see
// repoRootRelativePath), needed to match each returned Dirent.Path back
// to a bare child name.
func exportDir(c *svn.Client, svnPath, anchor string, rev *int, destPath string, stdout io.Writer) error {
	entries, err := c.List(svnPath, rev, "immediates", []string{"kind"})
	if err != nil {
		return err
	}
	fullDirPath := joinNonEmpty(anchor, svnPath)
	// Server.List always includes the queried directory itself as one of
	// the entries (see Server.List's own doc comment); filter it out by
	// this exact match, then strip the same prefix from every other
	// entry to recover its bare child name.
	selfPath := "/" + fullDirPath
	for _, entry := range entries {
		if entry.Path == selfPath {
			continue
		}
		name := strings.TrimPrefix(entry.Path, "/")
		if fullDirPath != "" {
			name = strings.TrimPrefix(name, fullDirPath+"/")
		}
		if name == "" {
			continue
		}
		childSvnPath, childDest := joinNonEmpty(svnPath, name), filepath.Join(destPath, name)
		switch entry.Kind {
		case "dir":
			if err := os.Mkdir(childDest, 0o755); err != nil {
				return fmt.Errorf("export: %w", err)
			}
			fmt.Fprintf(stdout, "A    %s\n", childDest)
			if err := exportDir(c, childSvnPath, anchor, rev, childDest, stdout); err != nil {
				return err
			}
		case "file":
			if err := exportFile(c, childSvnPath, rev, childDest); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "A    %s\n", childDest)
		default:
			return fmt.Errorf("export: %s: unsupported node kind %q", childSvnPath, entry.Kind)
		}
	}
	return nil
}

// exportFile writes svnPath's content (at rev) to destPath.
func exportFile(c *svn.Client, svnPath string, rev *int, destPath string) error {
	_, content, err := c.GetFile(svnPath, rev, false, true)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, content, 0o644)
}

func help(stdout io.Writer) {
	fmt.Fprintln(stdout, `usage: go-svn [-v] [-r revision[:revision2]] <subcommand> <repo>

Available subcommands:
   info
   cat
   ls
   log
   export <repo> [localdir]

go-svn is a client for the Subversion protocol.`)
}
