package main

import (
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cespedes/svn"
)

func main() {
	err := run(os.Args, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err.Error())
		os.Exit(1)
	}
}

// run dispatches to the subcommand named by args[1], mirroring a real
// "svn"'s own command-line shape: options like "-r"/"-v" go after the
// subcommand name (e.g. "go-svn cat -r5 URL"), not before it, and only
// the subcommands that actually use a given option accept it at all --
// see parseArgs.
func run(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("type 'go-svn help' for usage")
	}
	cmdName := args[1]
	rest := args[2:]

	if cmdName == "help" {
		help(stdout)
		return nil
	}

	switch cmdName {
	case "info":
		positional, lrev1, lrev2, _, _, err := parseArgs(rest, true, false, false)
		if err != nil {
			return err
		}
		if len(positional) != 1 {
			return errors.New("subcommand 'info' takes exactly one argument (repo URL)")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'info' does not accept a revision range")
		}
		return svnInfo(positional[0], lrev1, stdout)
	case "cat":
		positional, lrev1, lrev2, _, _, err := parseArgs(rest, true, false, false)
		if err != nil {
			return err
		}
		if len(positional) != 1 {
			return errors.New("subcommand 'cat' takes exactly one argument (repo URL)")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'cat' does not accept a revision range")
		}
		return svnCat(positional[0], lrev1, stdout)
	case "ls":
		positional, lrev1, lrev2, verbose, _, err := parseArgs(rest, true, true, false)
		if err != nil {
			return err
		}
		if len(positional) != 1 {
			return errors.New("subcommand 'ls' takes exactly one argument (repo URL)")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'ls' does not accept a revision range")
		}
		return svnLs(positional[0], lrev1, verbose, stdout)
	case "log":
		positional, lrev1, lrev2, verbose, _, err := parseArgs(rest, true, true, false)
		if err != nil {
			return err
		}
		if len(positional) != 1 {
			return errors.New("subcommand 'log' takes exactly one argument (repo URL)")
		}
		return svnLog(positional[0], lrev1, lrev2, verbose, stdout)
	case "export":
		positional, lrev1, lrev2, _, _, err := parseArgs(rest, true, false, false)
		if err != nil {
			return err
		}
		if len(positional) < 1 || len(positional) > 2 {
			return errors.New("subcommand 'export' takes a repo URL and, optionally, a destination directory")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'export' does not accept a revision range")
		}
		dest := ""
		if len(positional) == 2 {
			dest = positional[1]
		}
		return svnExport(positional[0], lrev1, dest, stdout)
	case "checkout":
		positional, lrev1, lrev2, _, _, err := parseArgs(rest, true, false, false)
		if err != nil {
			return err
		}
		if len(positional) < 1 || len(positional) > 2 {
			return errors.New("subcommand 'checkout' takes a repo URL and, optionally, a local directory")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'checkout' does not accept a revision range")
		}
		dest := ""
		if len(positional) == 2 {
			dest = positional[1]
		}
		return svnCheckout(positional[0], lrev1, dest, stdout)
	case "update":
		positional, lrev1, lrev2, _, _, err := parseArgs(rest, true, false, false)
		if err != nil {
			return err
		}
		if len(positional) != 1 {
			return errors.New("subcommand 'update' takes exactly one argument (a local directory 'checkout' produced)")
		}
		if lrev2 != nil {
			return errors.New("subcommand 'update' does not accept a revision range")
		}
		return svnUpdate(positional[0], lrev1, stdout)
	case "commit":
		positional, _, _, _, message, err := parseArgs(rest, false, false, true)
		if err != nil {
			return err
		}
		if len(positional) != 1 {
			return errors.New("subcommand 'commit' takes exactly one argument (a local directory 'checkout' produced)")
		}
		if message == "" {
			return errors.New("subcommand 'commit' requires a commit message: use '-m message'")
		}
		return svnCommit(positional[0], message, stdout)
	default:
		return fmt.Errorf(`unknown subcommand: '%s'
Type 'go-svn help' for usage`, cmdName)
	}
}

// parseArgs scans args (everything after the subcommand name) for "-r"/
// "-v"/"-m" options mixed in among positional arguments, the same way a
// real "svn" subcommand accepts them -- e.g. "svn cat -r5 URL" or
// "svn cat URL -r5", not just options before the subcommand name.
// acceptRev/acceptVerbose/acceptMessage report whether this particular
// subcommand accepts "-r"/"-v"/"-m" at all: passing one it doesn't is
// reported as an error here, the same way a real "svn" subcommand
// rejects an option it doesn't support, rather than silently ignored.
// "-r"/"-m" each take their value either joined ("-r5", "-mfix bug") or
// as a separate argument ("-r 5", "-m fix bug"); "-v" takes no value.
func parseArgs(args []string, acceptRev, acceptVerbose, acceptMessage bool) (positional []string, rev1, rev2 *int, verbose bool, message string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-r":
			if !acceptRev {
				return nil, nil, nil, false, "", fmt.Errorf("this subcommand does not accept option '-r'")
			}
			i++
			if i >= len(args) {
				return nil, nil, nil, false, "", errors.New("option '-r' expects an argument")
			}
			if rev1, rev2, err = parseRevArg(args[i]); err != nil {
				return nil, nil, nil, false, "", err
			}
		case strings.HasPrefix(arg, "-r") && arg != "-r":
			if !acceptRev {
				return nil, nil, nil, false, "", fmt.Errorf("this subcommand does not accept option '-r'")
			}
			if rev1, rev2, err = parseRevArg(strings.TrimPrefix(arg, "-r")); err != nil {
				return nil, nil, nil, false, "", err
			}
		case arg == "-v":
			if !acceptVerbose {
				return nil, nil, nil, false, "", fmt.Errorf("this subcommand does not accept option '-v'")
			}
			verbose = true
		case arg == "-m":
			if !acceptMessage {
				return nil, nil, nil, false, "", fmt.Errorf("this subcommand does not accept option '-m'")
			}
			i++
			if i >= len(args) {
				return nil, nil, nil, false, "", errors.New("option '-m' expects an argument")
			}
			message = args[i]
		case strings.HasPrefix(arg, "-m") && arg != "-m":
			if !acceptMessage {
				return nil, nil, nil, false, "", fmt.Errorf("this subcommand does not accept option '-m'")
			}
			message = strings.TrimPrefix(arg, "-m")
		case strings.HasPrefix(arg, "-") && arg != "-":
			return nil, nil, nil, false, "", fmt.Errorf("unknown option: %s", arg)
		default:
			positional = append(positional, arg)
		}
	}
	return positional, rev1, rev2, verbose, message, nil
}

// parseRevArg parses s -- the value following "-r", or the remainder of
// a joined "-rN"/"-rN:M" option -- the same way "svn"'s own "-r"
// argument works: either a single revision ("5") or a range ("5:10").
func parseRevArg(s string) (rev1, rev2 *int, err error) {
	s1, s2, found := strings.Cut(s, ":")
	r1, err := parseRevNumber(s1)
	if err != nil {
		return nil, nil, err
	}
	rev1 = &r1
	if found {
		r2, err := parseRevNumber(s2)
		if err != nil {
			return nil, nil, err
		}
		rev2 = &r2
	}
	return rev1, rev2, nil
}

// parseRevNumber parses s as a single revision number: a non-negative
// integer. "svn"'s own "-r" also accepts symbolic revisions ("HEAD",
// "BASE", ...), none of which this package's Client resolves, so those
// aren't accepted here either -- rejected the same way a negative
// number is, rather than silently accepted and sent to the server as
// some other, wildly different revision (a plain Go int, negative or
// not, marshals as an unsigned wire number, so a negative one would
// otherwise wrap around into a huge, nonsensical revision instead of
// failing clearly).
func parseRevNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid revision %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid revision %q: revision numbers must not be negative", s)
	}
	return n, nil
}

// formatDate parses s -- a Dirent/Stat/LogEntry's own CreatedDate/Date
// field, always an ISO 8601 timestamp in UTC (e.g.
// "2024-04-02T13:37:34.350221Z") -- and formats it as "yyyy-mm-dd
// hh:mm:ss" in the local time zone, the same shape every subcommand
// that prints a date uses. s is returned unchanged if it doesn't parse
// (e.g. empty, for a node with no recorded date).
func formatDate(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func svnInfo(repo string, lrev *int, stdout io.Writer) error {
	c, err := svn.Connect(repo)

	if err != nil {
		return err
	}

	rev := 0
	if lrev != nil {
		rev = *lrev
	} else {
		rev, err = c.GetLatestRev()
		if err != nil {
			return err
		}
	}

	stat, err := c.Stat("", lrev)
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
	fmt.Fprintf(stdout, "Last Changed Date: %s\n", formatDate(stat.CreatedDate))

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
			date := formatDate(entry.CreatedDate)
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

	// A single "-rN" (no ":N2" range) means exactly revision N, the same
	// way a real "svn log -rN" behaves -- not "from N down to the
	// beginning of history", which is what leaving lrev2 nil would mean
	// to Client.Log (a nil endRev defaults to revision 0).
	if lrev1 != nil && lrev2 == nil {
		lrev2 = lrev1
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
		fmt.Fprintf(stdout, "r%d | %s | %s | %s\n", l.Rev, l.Author, formatDate(l.Date), slines)
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

// checkoutInfoFile is the name of a small sidecar file "checkout"/
// "update" use to remember which repository URL and revision a plain,
// unversioned local directory (see diskEditor) was last brought to --
// nothing like a real ".svn" working copy database, just enough for a
// later "go-svn update <localdir>" to know what to ask for without
// requiring the URL again. This is entirely a go-svn implementation
// detail: the svn package itself has no notion of a working copy, or of
// a filesystem, at all -- see svn.Editor's own doc comment.
const checkoutInfoFile = ".go-svn-checkout"

// checkoutInfo is checkoutInfoFile's own parsed content.
type checkoutInfo struct {
	URL string
	Rev int
}

// readCheckoutInfo reads dir's own checkoutInfoFile, written by an
// earlier "checkout" or "update" of it.
func readCheckoutInfo(dir string) (checkoutInfo, error) {
	data, err := os.ReadFile(filepath.Join(dir, checkoutInfoFile))
	if err != nil {
		return checkoutInfo{}, fmt.Errorf("update: %s: %w (was it checked out with 'go-svn checkout'?)", dir, err)
	}
	lines := strings.SplitN(strings.TrimRight(string(data), "\n"), "\n", 2)
	if len(lines) != 2 {
		return checkoutInfo{}, fmt.Errorf("update: %s: malformed checkout info", filepath.Join(dir, checkoutInfoFile))
	}
	rev, err := strconv.Atoi(lines[1])
	if err != nil {
		return checkoutInfo{}, fmt.Errorf("update: %s: malformed checkout info: %w", filepath.Join(dir, checkoutInfoFile), err)
	}
	return checkoutInfo{URL: lines[0], Rev: rev}, nil
}

// writeCheckoutInfo writes dir's own checkoutInfoFile.
func writeCheckoutInfo(dir string, info checkoutInfo) error {
	data := fmt.Sprintf("%s\n%d\n", info.URL, info.Rev)
	if err := os.WriteFile(filepath.Join(dir, checkoutInfoFile), []byte(data), 0o644); err != nil {
		return fmt.Errorf("writing checkout info: %w", err)
	}
	return nil
}

// svnCheckout checks out repo at lrev (nil meaning the latest revision)
// into dest (the last path segment of repo's own URL if empty, matching
// a real "svn checkout") by driving svn.Client.Checkout -- the real
// report/editor exchange, unlike svnExport's own recursive List/GetFile
// walk -- with a diskEditor writing every node into dest. Remembers
// repo's own URL and the checked-out revision in dest's own
// checkoutInfoFile, so a later "go-svn update dest" knows what to ask
// for.
func svnCheckout(repo string, lrev *int, dest string, stdout io.Writer) error {
	c, err := svn.Connect(repo)
	if err != nil {
		return err
	}

	// A real "svn checkout" only ever supports a directory (there's no
	// wire-level reason to reject a file, but a real client's own
	// working-copy layer refuses the request up front rather than
	// producing whatever an editor sequence anchored at a file might
	// mean), and obviously requires the URL to exist at all -- both
	// checked here, with wording that mirrors a real client's own
	// messages, rather than letting either case reach Client.Checkout:
	// doing so instead surfaces as a confusing, low-level svnserve
	// report-processing error ("160005 Cannot replace a directory from
	// within" for a file URL, "160005 Target path '...' does not exist"
	// for one that doesn't exist at all), since nothing about the
	// report/editor exchange itself is actually invalid in either case.
	stat, err := c.Stat("", lrev)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("checkout: URL '%s' does not exist", repo)
		}
		return err
	}
	if stat.Kind != "dir" {
		return fmt.Errorf("checkout: URL '%s' refers to a %s, not a directory", repo, stat.Kind)
	}

	if dest == "" {
		dest, err = defaultExportDest(repo)
		if err != nil {
			return err
		}
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		return fmt.Errorf("checkout: %w", err)
	}

	rev, err := c.Checkout(lrev, diskEditor(dest, stdout))
	if err != nil {
		return err
	}
	if err := writeCheckoutInfo(dest, checkoutInfo{URL: repo, Rev: rev}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Checked out revision %d.\n", rev)
	return nil
}

// svnUpdate brings dest (an existing directory an earlier "go-svn
// checkout" produced) up to lrev (nil meaning the latest revision), by
// driving svn.Client.Update against the same repository URL dest's own
// checkoutInfoFile recorded, with a diskEditor applying whatever changed
// directly onto dest. Updates checkoutInfoFile to the new revision
// afterwards.
func svnUpdate(dest string, lrev *int, stdout io.Writer) error {
	info, err := readCheckoutInfo(dest)
	if err != nil {
		return err
	}

	c, err := svn.Connect(info.URL)
	if err != nil {
		return err
	}

	rev, err := c.Update(info.Rev, lrev, diskEditor(dest, stdout))
	if err != nil {
		return err
	}
	if err := writeCheckoutInfo(dest, checkoutInfo{URL: info.URL, Rev: rev}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Updated to revision %d.\n", rev)
	return nil
}

// remoteChildKinds lists the direct children of svnPath (at rev) in the
// repository, keyed by their own bare name and mapping to "dir" or
// "file" -- the same self-entry filtering and repository-root-relative-
// to-bare-name stripping svnExport's own exportDir needs (see
// repoRootRelativePath), just against a single, concrete revision
// rather than exportDir's own "latest if nil" one, since svnCommit
// always compares against the checkout's own known base revision.
func remoteChildKinds(c *svn.Client, svnPath, anchor string, rev int) (map[string]string, error) {
	entries, err := c.List(svnPath, &rev, "immediates", []string{"kind"})
	if err != nil {
		return nil, err
	}
	fullDirPath := joinNonEmpty(anchor, svnPath)
	selfPath := "/" + fullDirPath
	kinds := map[string]string{}
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
		kinds[name] = entry.Kind
	}
	return kinds, nil
}

// localChildKinds lists localDir's own direct entries, keyed by name and
// mapping to "dir" or "file" (anything that isn't a directory is treated
// as a plain file), skipping checkoutInfoFile -- the sidecar bookkeeping
// file svnCommit must never treat as repository content.
func localChildKinds(localDir string) (map[string]string, error) {
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	for _, entry := range entries {
		if entry.Name() == checkoutInfoFile {
			continue
		}
		if entry.IsDir() {
			kinds[entry.Name()] = "dir"
		} else {
			kinds[entry.Name()] = "file"
		}
	}
	return kinds, nil
}

// checksum returns content's MD5 checksum as the lowercase hex string
// EditorWriter.ApplyTextdelta/CloseFile expect.
func checksum(content []byte) []byte {
	return []byte(fmt.Sprintf("%x", md5.Sum(content)))
}

// commitChange is one child node svnCommit's own tree comparison found
// needing description to the server: a brand new node ("add"), a
// removed one ("delete"), a modified file's new content ("modify"), or
// an existing, unmodified-itself directory that still needs opening
// because something changed somewhere inside it ("visit"). children
// holds nested commitChanges for an "add"ed or "visit"ed directory;
// planDir/planNewTree never produce an entry for a node that turned out
// entirely unchanged, so a "visit" node's own children is never empty
// (planDir only ever emits "visit" for a subdirectory whose nested plan
// came back non-empty).
type commitChange struct {
	name         string
	isDir        bool
	action       string // "add", "delete", "modify", "visit"
	content      []byte // new content, for "add"/"modify" of a file
	baseChecksum []byte // remote content's own checksum, for "modify"
	openRev      int    // revision to OpenDir/OpenFile at, for "modify"/"visit"
	children     []commitChange
}

// planNewTree walks localDir -- entirely new, nothing on the server
// side to compare against -- describing every file and subdirectory
// inside it as "add", recursively.
func planNewTree(localDir string) ([]commitChange, error) {
	kinds, err := localChildKinds(localDir)
	if err != nil {
		return nil, err
	}
	var changes []commitChange
	for name, kind := range kinds {
		childLocal := filepath.Join(localDir, name)
		if kind == "dir" {
			children, err := planNewTree(childLocal)
			if err != nil {
				return nil, err
			}
			changes = append(changes, commitChange{name: name, isDir: true, action: "add", children: children})
		} else {
			content, err := os.ReadFile(childLocal)
			if err != nil {
				return nil, err
			}
			changes = append(changes, commitChange{name: name, action: "add", content: content})
		}
	}
	return changes, nil
}

// addChange builds the commitChange for a brand new node named name,
// found locally at childLocal -- a directory's own content is described
// via planNewTree.
func addChange(name, localKind, childLocal string) (commitChange, error) {
	if localKind == "dir" {
		children, err := planNewTree(childLocal)
		if err != nil {
			return commitChange{}, err
		}
		return commitChange{name: name, isDir: true, action: "add", children: children}, nil
	}
	content, err := os.ReadFile(childLocal)
	if err != nil {
		return commitChange{}, err
	}
	return commitChange{name: name, action: "add", content: content}, nil
}

// planDir compares localDir (a directory an earlier "checkout"/"update"
// produced) against the repository's own directory at wirePath (as of
// rev, the checkout's own base revision -- svnCommit only supports
// committing against a single, uniform base, the same limitation every
// other single-revision operation in this package has), returning one
// commitChange per child that needs describing to the server. A child
// present, unchanged, and of the same kind on both sides is left out
// entirely -- including a directory whose own contents didn't change --
// so an untouched subtree is never even opened on the wire.
//
// Unlike a real "svn commit", there is no staged add/remove/schedule
// step at all: this simply compares the two trees and sends whatever
// differs, matching this package's broader "no working-copy metadata"
// stance (see checkoutInfoFile's own doc comment). A file dropped into
// dest by any means, not just "checkout"/"update", is committed as a
// new addition the same way an svn client's own "svn add"ed file would
// be.
func planDir(c *svn.Client, localDir, wirePath, anchor string, rev int) ([]commitChange, error) {
	remoteKinds, err := remoteChildKinds(c, wirePath, anchor, rev)
	if err != nil {
		return nil, err
	}
	localKinds, err := localChildKinds(localDir)
	if err != nil {
		return nil, err
	}

	names := map[string]bool{}
	for name := range remoteKinds {
		names[name] = true
	}
	for name := range localKinds {
		names[name] = true
	}

	var changes []commitChange
	for name := range names {
		localKind, hasLocal := localKinds[name]
		remoteKind, hasRemote := remoteKinds[name]
		childLocal := filepath.Join(localDir, name)
		childWire := joinNonEmpty(wirePath, name)

		switch {
		case !hasRemote:
			change, err := addChange(name, localKind, childLocal)
			if err != nil {
				return nil, err
			}
			changes = append(changes, change)
		case !hasLocal:
			changes = append(changes, commitChange{name: name, isDir: remoteKind == "dir", action: "delete"})
		case localKind != remoteKind:
			// The old node must be deleted before the new one of a
			// different kind can be added in its place -- see
			// EditorWriter.DeleteEntry's own doc comment.
			changes = append(changes, commitChange{name: name, isDir: remoteKind == "dir", action: "delete"})
			change, err := addChange(name, localKind, childLocal)
			if err != nil {
				return nil, err
			}
			changes = append(changes, change)
		case localKind == "dir":
			nested, err := planDir(c, childLocal, childWire, anchor, rev)
			if err != nil {
				return nil, err
			}
			if len(nested) > 0 {
				changes = append(changes, commitChange{name: name, isDir: true, action: "visit", openRev: rev, children: nested})
			}
		default:
			localContent, err := os.ReadFile(childLocal)
			if err != nil {
				return nil, err
			}
			_, remoteContent, err := c.GetFile(childWire, &rev, false, true)
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(localContent, remoteContent) {
				changes = append(changes, commitChange{
					name: name, action: "modify", content: localContent,
					baseChecksum: checksum(remoteContent), openRev: rev,
				})
			}
		}
	}
	return changes, nil
}

// emitChanges drives e to describe every change in changes -- and,
// recursively, everything nested under an "add"ed or "visit"ed
// directory -- printing one "A"/"D"/"U" status line per file or
// directory touched, the same letters diskEditor already prints for
// checkout/update.
func emitChanges(e *svn.EditorWriter, changes []commitChange, wirePath string, stdout io.Writer) error {
	for _, ch := range changes {
		childWire := joinNonEmpty(wirePath, ch.name)
		switch ch.action {
		case "delete":
			if err := e.DeleteEntry(childWire, nil); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "D    %s\n", childWire)
		case "add":
			if ch.isDir {
				if err := e.AddDir(childWire, nil); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "A    %s\n", childWire)
				if err := emitChanges(e, ch.children, childWire, stdout); err != nil {
					return err
				}
				if err := e.CloseDir(); err != nil {
					return err
				}
			} else {
				if err := e.AddFile(childWire, nil); err != nil {
					return err
				}
				if err := e.ApplyTextdelta(ch.content, nil); err != nil {
					return err
				}
				if err := e.CloseFile(checksum(ch.content)); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "A    %s\n", childWire)
			}
		case "modify":
			if err := e.OpenFile(childWire, uint(ch.openRev)); err != nil {
				return err
			}
			if err := e.ApplyTextdelta(ch.content, ch.baseChecksum); err != nil {
				return err
			}
			if err := e.CloseFile(checksum(ch.content)); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "U    %s\n", childWire)
		case "visit":
			if err := e.OpenDir(childWire, uint(ch.openRev)); err != nil {
				return err
			}
			if err := emitChanges(e, ch.children, childWire, stdout); err != nil {
				return err
			}
			if err := e.CloseDir(); err != nil {
				return err
			}
		}
	}
	return nil
}

// svnCommit sends every local change under dest (a directory an earlier
// "go-svn checkout" produced) back to the repository as a new revision,
// using dest's own checkoutInfoFile to know both the repository URL and
// the base revision to compare against -- like "go-svn update", only a
// single, uniform base revision is supported, not a "mixed revision"
// working copy. Unlike checkout/update, which drive svn.Client through
// a real report/editor exchange, there is no report here at all: the
// whole tree of changes is built up front by comparing dest's own
// current content (planDir/planNewTree) against the repository at that
// base revision, into a []svn.Item via svn.EditorWriter (the same
// builder svn.Server's own FinishReport callbacks use server-side, just
// handed to svn.Client.Commit instead of returned from a callback), then
// sent in a single call.
func svnCommit(dest string, message string, stdout io.Writer) error {
	info, err := readCheckoutInfo(dest)
	if err != nil {
		return err
	}

	c, err := svn.Connect(info.URL)
	if err != nil {
		return err
	}

	anchor, err := repoRootRelativePath(c, info.URL)
	if err != nil {
		return err
	}

	changes, err := planDir(c, dest, "", anchor, info.Rev)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintln(stdout, "Nothing to commit.")
		return nil
	}

	e := svn.NewEditorWriter()
	if err := e.OpenRoot(nil); err != nil {
		return err
	}
	if err := emitChanges(e, changes, "", stdout); err != nil {
		return err
	}
	if err := e.CloseDir(); err != nil {
		return err
	}
	items, err := e.Items()
	if err != nil {
		return err
	}

	result, err := c.Commit(message, items)
	if err != nil {
		return err
	}

	if err := writeCheckoutInfo(dest, checkoutInfo{URL: info.URL, Rev: result.Rev}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Committed revision %d.\n", result.Rev)
	return nil
}

// diskEditor returns an svn.Editor that creates, modifies and removes
// real files/directories under dest to match whatever
// svn.Client.Checkout/Update describes, printing one status line per
// file touched ("A" added, "U" updated, "D" removed), the same letters a
// real "svn checkout"/"svn update" prints. This -- not anything in the
// svn package itself -- is what decides that "checking out" or
// "updating" means writing to the local filesystem; see svn.Editor's own
// doc comment for why that decision belongs here, in the command-line
// tool, rather than in the library.
func diskEditor(dest string, stdout io.Writer) svn.Editor {
	// added tracks, by path, a file most recently described via AddFile
	// rather than OpenFile -- purely so CloseFile below can print "A"
	// instead of "U" for it, matching a real client's own output.
	added := map[string]bool{}
	local := func(wirePath string) (string, error) {
		for _, seg := range strings.Split(wirePath, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return "", fmt.Errorf("checkout: unsafe path %q in editor command", wirePath)
			}
		}
		return filepath.Join(dest, filepath.FromSlash(wirePath)), nil
	}

	return svn.Editor{
		AddDir: func(path string, copyFrom *svn.EditorCopyFrom) error {
			p, err := local(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "A    %s\n", p)
			return nil
		},
		OpenDir: func(path string, rev int) error {
			p, err := local(path)
			if err != nil {
				return err
			}
			return os.MkdirAll(p, 0o755)
		},
		DeleteEntry: func(path string, rev *int) error {
			p, err := local(path)
			if err != nil {
				return err
			}
			if err := os.RemoveAll(p); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "D    %s\n", p)
			return nil
		},
		AddFile: func(path string, copyFrom *svn.EditorCopyFrom) error {
			added[path] = true
			return nil
		},
		OpenFile: func(path string, rev int) ([]byte, error) {
			p, err := local(path)
			if err != nil {
				return nil, err
			}
			return os.ReadFile(p)
		},
		CloseFile: func(path string, content []byte) error {
			p, err := local(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(p, content, 0o644); err != nil {
				return err
			}
			letter := "U"
			if added[path] {
				letter = "A"
				delete(added, path)
			}
			fmt.Fprintf(stdout, "%s    %s\n", letter, p)
			return nil
		},
	}
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
	fmt.Fprintln(stdout, `usage: go-svn <subcommand> [-r revision[:revision2]] [-v] <repo>

Options go after the subcommand name and before <repo> (e.g.
"go-svn cat -r100 URL"), and only the subcommands that use a given
option accept it at all.

Available subcommands:
   info [-r rev] <repo>
   cat [-r rev] <repo>
   ls [-r rev] [-v] <repo>
   log [-r rev[:rev2]] [-v] <repo>
   export [-r rev] <repo> [localdir]
   checkout [-r rev] <repo> [localdir]
   update [-r rev] <localdir>
   commit -m message <localdir>

go-svn is a client for the Subversion protocol.`)
}
