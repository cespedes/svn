package main

// Integration tests that run go-svn's own subcommands against a real,
// temporary svnadmin-created repository (exec'ing a real svnserve under
// the hood, via svn.Connect's "file://" handling). They skip cleanly if
// svnadmin/svn/svnserve aren't on PATH.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// requireRealSVNTools skips the test unless svnadmin, svn and svnserve
// are all available on PATH.
func requireRealSVNTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"svnadmin", "svn", "svnserve"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found in PATH; skipping test against a real svnserve", tool)
		}
	}
}

// newExportTestRepo creates a temporary repository with a real svnadmin,
// populates it with a nested tree over one commit, and returns its
// file:// URL. Layout:
//
//	README.md
//	trunk/main.go
//	trunk/sub/nested.txt
func newExportTestRepo(t *testing.T) string {
	t.Helper()
	requireRealSVNTools(t)

	repoPath := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("svnadmin", "create", repoPath).CombinedOutput(); err != nil {
		t.Fatalf("svnadmin create: %v\n%s", err, out)
	}
	repoURL := "file://" + repoPath

	wc := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		args = append([]string{"--non-interactive"}, args...)
		cmd := exec.Command("svn", args...)
		cmd.Dir = wc
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("svn %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(wc, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("checkout", "-q", repoURL, ".")
	write("README.md", "hello world\n")
	write("trunk/main.go", "package main\n")
	write("trunk/sub/nested.txt", "nested\n")
	run("add", "-q", "README.md", "trunk")
	run("commit", "-q", "-m", "initial commit")

	return repoURL
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}

// TestExport checks "go-svn export" against a real svnserve: a plain
// export of the whole repository, an export anchored below the
// repository root (which -- confirmed against a real svnserve -- returns
// every Dirent.Path relative to the repository root regardless of the
// session's own anchor, unlike every List/GetFile path argument the
// caller sends, which stays anchor-relative; see repoRootRelativePath),
// and a single-file export.
func TestExport(t *testing.T) {
	repoURL := newExportTestRepo(t)
	tmp := t.TempDir()

	t.Run("whole repository", func(t *testing.T) {
		dest := filepath.Join(tmp, "root")
		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL, dest}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, filepath.Join(dest, "README.md")); got != "hello world\n" {
			t.Errorf("README.md = %q, want %q", got, "hello world\n")
		}
		if got := readFile(t, filepath.Join(dest, "trunk", "main.go")); got != "package main\n" {
			t.Errorf("trunk/main.go = %q, want %q", got, "package main\n")
		}
		if got := readFile(t, filepath.Join(dest, "trunk", "sub", "nested.txt")); got != "nested\n" {
			t.Errorf("trunk/sub/nested.txt = %q, want %q", got, "nested\n")
		}
		if _, err := os.Stat(filepath.Join(dest, ".svn")); err == nil {
			t.Errorf("export left a .svn directory behind")
		}
	})

	t.Run("anchored below the repository root", func(t *testing.T) {
		dest := filepath.Join(tmp, "trunk-only")
		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL + "/trunk", dest}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, filepath.Join(dest, "main.go")); got != "package main\n" {
			t.Errorf("main.go = %q, want %q", got, "package main\n")
		}
		if got := readFile(t, filepath.Join(dest, "sub", "nested.txt")); got != "nested\n" {
			t.Errorf("sub/nested.txt = %q, want %q", got, "nested\n")
		}
		if _, err := os.Stat(filepath.Join(dest, "trunk")); err == nil {
			t.Fatalf("export created a spurious nested %q directory (the self-entry was mistaken for a child)", filepath.Join(dest, "trunk"))
		}
	})

	t.Run("single file", func(t *testing.T) {
		dest := filepath.Join(tmp, "readme-only.txt")
		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL + "/README.md", dest}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, dest); got != "hello world\n" {
			t.Errorf("content = %q, want %q", got, "hello world\n")
		}
	})

	t.Run("default destination derived from the URL", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)

		var out bytes.Buffer
		if err := run([]string{"go-svn", "export", repoURL + "/trunk"}, &out); err != nil {
			t.Fatalf("export: %v\noutput:\n%s", err, out.String())
		}
		if got := readFile(t, filepath.Join(dir, "trunk", "main.go")); got != "package main\n" {
			t.Errorf("trunk/main.go = %q, want %q", got, "package main\n")
		}
	})
}

// TestCLIOptionOrdering checks that "-r"/"-v" only work after the
// subcommand name, mirroring a real "svn"'s own command-line shape
// (e.g. "svn cat -r5 URL", not "svn -r5 cat URL"), and that a joined
// "-rN" works the same as a separate "-r N".
func TestCLIOptionOrdering(t *testing.T) {
	requireRealSVNTools(t)

	repoPath := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("svnadmin", "create", repoPath).CombinedOutput(); err != nil {
		t.Fatalf("svnadmin create: %v\n%s", err, out)
	}
	repoURL := "file://" + repoPath

	wc := t.TempDir()
	svnCmd := func(args ...string) {
		t.Helper()
		args = append([]string{"--non-interactive"}, args...)
		cmd := exec.Command("svn", args...)
		cmd.Dir = wc
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("svn %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(wc, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svnCmd("checkout", "-q", repoURL, ".")
	write("main.go", "package main\n")
	svnCmd("add", "-q", "main.go")
	svnCmd("commit", "-q", "-m", "r1")
	write("main.go", "package main\n\nfunc main() {}\n")
	svnCmd("commit", "-q", "-m", "r2")
	write("main.go", "package main\n\nfunc main() { println(\"hi\") }\n")
	svnCmd("commit", "-q", "-m", "r3")

	t.Run("-r after the subcommand, joined", func(t *testing.T) {
		var out bytes.Buffer
		if err := run([]string{"go-svn", "cat", "-r1", repoURL + "/main.go"}, &out); err != nil {
			t.Fatalf("cat: %v", err)
		}
		if out.String() != "package main\n" {
			t.Errorf("cat -r1 = %q, want %q", out.String(), "package main\n")
		}
	})

	t.Run("-r after the subcommand, separate", func(t *testing.T) {
		var out bytes.Buffer
		if err := run([]string{"go-svn", "cat", "-r", "1", repoURL + "/main.go"}, &out); err != nil {
			t.Fatalf("cat: %v", err)
		}
		if out.String() != "package main\n" {
			t.Errorf("cat -r 1 = %q, want %q", out.String(), "package main\n")
		}
	})

	t.Run("-r before the subcommand is rejected", func(t *testing.T) {
		var out bytes.Buffer
		err := run([]string{"go-svn", "-r1", "cat", repoURL + "/main.go"}, &out)
		if err == nil {
			t.Fatalf("expected an error, got none (output: %s)", out.String())
		}
		if !strings.Contains(err.Error(), "unknown subcommand") {
			t.Errorf("error = %q, want it to mention an unknown subcommand", err.Error())
		}
	})

	t.Run("-v rejected by a subcommand that doesn't accept it", func(t *testing.T) {
		var out bytes.Buffer
		err := run([]string{"go-svn", "cat", "-v", repoURL + "/main.go"}, &out)
		if err == nil {
			t.Fatalf("expected an error, got none (output: %s)", out.String())
		}
	})

	// Reported as a real bug: "info" accepted "-r" (unlike, say, a
	// subcommand that rejects it outright) but silently ignored it,
	// always reporting the latest revision regardless.
	t.Run("info -r actually affects the reported revision", func(t *testing.T) {
		var out bytes.Buffer
		if err := run([]string{"go-svn", "info", "-r1", repoURL}, &out); err != nil {
			t.Fatalf("info: %v", err)
		}
		if !strings.Contains(out.String(), "Revision: 1") {
			t.Errorf("info -r1 output missing \"Revision: 1\":\n%s", out.String())
		}
		if !strings.Contains(out.String(), "Last Changed Rev: 1") {
			t.Errorf("info -r1 output missing \"Last Changed Rev: 1\":\n%s", out.String())
		}

		out.Reset()
		if err := run([]string{"go-svn", "info", repoURL}, &out); err != nil {
			t.Fatalf("info: %v", err)
		}
		if !strings.Contains(out.String(), "Revision: 3") {
			t.Errorf("info (no -r) output missing \"Revision: 3\":\n%s", out.String())
		}
	})

	// Reported as a real bug: a real "svn log -rN" (no ":N2" range)
	// shows exactly that one revision, but "go-svn log -rN" showed N
	// and every revision before it too, down to the beginning of
	// history -- because a bare "-rN" left the log's own end-revision
	// nil, which Client.Log treats as "revision 0" (the same convention
	// every *other* subcommand's single "-rN" correctly relies on, since
	// they only ever have one revision slot to begin with).
	t.Run("log -rN shows only revision N, not everything before it too", func(t *testing.T) {
		var out bytes.Buffer
		if err := run([]string{"go-svn", "log", "-r2", repoURL}, &out); err != nil {
			t.Fatalf("log: %v", err)
		}
		if !strings.Contains(out.String(), "r2") {
			t.Errorf("log -r2 output missing r2:\n%s", out.String())
		}
		if strings.Contains(out.String(), "r1") {
			t.Errorf("log -r2 output should not include r1:\n%s", out.String())
		}
		if strings.Contains(out.String(), "r3") {
			t.Errorf("log -r2 output should not include r3:\n%s", out.String())
		}
	})

	t.Run("log -rN:M still shows the whole range", func(t *testing.T) {
		var out bytes.Buffer
		if err := run([]string{"go-svn", "log", "-r1:3", repoURL}, &out); err != nil {
			t.Fatalf("log: %v", err)
		}
		for _, want := range []string{"r1", "r2", "r3"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("log -r1:3 output missing %s:\n%s", want, out.String())
			}
		}
	})

	// Reported as a real bug: "info"/"log" printed a commit's date as
	// the raw ISO 8601 string the wire protocol uses ("2024-04-02T13:37:
	// 34.350221Z"), while "ls -v" instead reformatted it by slicing
	// ("2024-04-02 13:37:34", still in UTC) -- two different shapes,
	// neither in the local time zone. All three now use formatDate,
	// giving the exact same "yyyy-mm-dd hh:mm:ss", in local time, for
	// the same commit.
	t.Run("info/log/ls -v show dates in the same format", func(t *testing.T) {
		dateRe := regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)

		var infoOut bytes.Buffer
		if err := run([]string{"go-svn", "info", "-r1", repoURL}, &infoOut); err != nil {
			t.Fatalf("info: %v", err)
		}
		infoDate := dateRe.FindString(infoOut.String())
		if infoDate == "" {
			t.Fatalf("info output has no yyyy-mm-dd hh:mm:ss date:\n%s", infoOut.String())
		}

		var logOut bytes.Buffer
		if err := run([]string{"go-svn", "log", "-r1", repoURL}, &logOut); err != nil {
			t.Fatalf("log: %v", err)
		}
		if logDate := dateRe.FindString(logOut.String()); logDate != infoDate {
			t.Errorf("log date = %q, want the same as info's own %q", logDate, infoDate)
		}

		var lsOut bytes.Buffer
		if err := run([]string{"go-svn", "ls", "-v", "-r1", repoURL}, &lsOut); err != nil {
			t.Fatalf("ls: %v", err)
		}
		if lsDate := dateRe.FindString(lsOut.String()); lsDate != infoDate {
			t.Errorf("ls -v date = %q, want the same as info's own %q", lsDate, infoDate)
		}
	})
}

// TestCheckoutAndUpdate checks "go-svn checkout"/"go-svn update" against
// a real svnserve: unlike "export" (a one-shot recursive List/GetFile
// walk), these drive a real report/editor exchange via
// svn.Client.Checkout/Update, with diskEditor as the only thing deciding
// that the result lands on the local filesystem -- see svn.Editor's own
// doc comment. "update" also exercises checkoutInfoFile, the sidecar
// this command uses in place of a real ".svn" working copy database to
// remember which URL/revision a directory was checked out at.
func TestCheckoutAndUpdate(t *testing.T) {
	requireRealSVNTools(t)

	repoPath := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("svnadmin", "create", repoPath).CombinedOutput(); err != nil {
		t.Fatalf("svnadmin create: %v\n%s", err, out)
	}
	repoURL := "file://" + repoPath

	wc := t.TempDir()
	svnCmd := func(args ...string) {
		t.Helper()
		args = append([]string{"--non-interactive"}, args...)
		cmd := exec.Command("svn", args...)
		cmd.Dir = wc
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("svn %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(wc, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svnCmd("checkout", "-q", repoURL, ".")
	write("trunk/main.go", "package main\n")
	svnCmd("add", "-q", "trunk")
	svnCmd("commit", "-q", "-m", "initial commit")

	dest := filepath.Join(t.TempDir(), "co")
	var out bytes.Buffer
	if err := run([]string{"go-svn", "checkout", repoURL, dest}, &out); err != nil {
		t.Fatalf("checkout: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Checked out revision 1.") {
		t.Errorf("checkout output missing revision summary:\n%s", out.String())
	}
	if got := readFile(t, filepath.Join(dest, "trunk", "main.go")); got != "package main\n" {
		t.Errorf("trunk/main.go = %q, want %q", got, "package main\n")
	}
	if _, err := os.Stat(filepath.Join(dest, ".svn")); err == nil {
		t.Errorf("checkout left a .svn directory behind")
	}

	write("trunk/main.go", "package main\n\nfunc main() {}\n")
	write("trunk/newfile.go", "package main\n")
	svnCmd("add", "-q", "trunk/newfile.go")
	svnCmd("commit", "-q", "-m", "v2")

	out.Reset()
	if err := run([]string{"go-svn", "update", dest}, &out); err != nil {
		t.Fatalf("update: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Updated to revision 2.") {
		t.Errorf("update output missing revision summary:\n%s", out.String())
	}
	if got := readFile(t, filepath.Join(dest, "trunk", "main.go")); got != "package main\n\nfunc main() {}\n" {
		t.Errorf("trunk/main.go = %q, want %q (r2's own content)", got, "package main\n\nfunc main() {}\n")
	}
	if got := readFile(t, filepath.Join(dest, "trunk", "newfile.go")); got != "package main\n" {
		t.Errorf("trunk/newfile.go = %q, want %q", got, "package main\n")
	}

	// "checkout" of a URL that names a file, not a directory: reported
	// as a real bug ("go-svn checkout" of a file URL surfaced only as a
	// confusing, low-level svnserve error, "160005 Cannot replace a
	// directory from within", instead of an early, clear one -- a real
	// "svn checkout" itself rejects this up front with "URL '...' refers
	// to a file, not a directory").
	t.Run("checkout of a file URL fails clearly", func(t *testing.T) {
		fileDest := filepath.Join(t.TempDir(), "co")
		var out bytes.Buffer
		err := run([]string{"go-svn", "checkout", repoURL + "/trunk/main.go", fileDest}, &out)
		if err == nil {
			t.Fatalf("checkout of a file URL succeeded, want an error\noutput:\n%s", out.String())
		}
		if !strings.Contains(err.Error(), "refers to a file, not a directory") {
			t.Errorf("error = %q, want it to mention the URL refers to a file, not a directory", err.Error())
		}
		if _, statErr := os.Stat(fileDest); statErr == nil {
			t.Errorf("checkout of a file URL should not have created %s", fileDest)
		}
	})

	t.Run("checkout of a nonexistent URL fails clearly", func(t *testing.T) {
		missingDest := filepath.Join(t.TempDir(), "co")
		var out bytes.Buffer
		err := run([]string{"go-svn", "checkout", repoURL + "/does-not-exist", missingDest}, &out)
		if err == nil {
			t.Fatalf("checkout of a nonexistent URL succeeded, want an error\noutput:\n%s", out.String())
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("error = %q, want it to mention the URL does not exist", err.Error())
		}
	})
}

// TestParseArgs checks parseArgs' own option handling against the shape
// a real "svn" subcommand accepts: "-r"/"-v" after the subcommand name
// (not before it, which run itself enforces by only ever calling
// parseArgs with the arguments following the subcommand), "-r" with
// either a joined ("-r5", "-r5:6") or separate ("-r", "5") value, and a
// subcommand rejecting an option it doesn't accept.
// TestFormatDate checks formatDate against the exact ISO 8601 shape
// Dirent/Stat/LogEntry's own CreatedDate/Date fields use (UTC, with
// fractional seconds), converting to the local time zone -- reported as
// a real bug: "info"/"log" printed that raw UTC string verbatim, while
// "ls -v" instead reformatted it by slicing (still in UTC, not the
// local zone), so the three subcommands showed dates in two different
// shapes, neither of them in local time.
func TestFormatDate(t *testing.T) {
	const in = "2024-04-02T13:37:34.350221Z"
	got := formatDate(in)

	want, err := time.Parse(time.RFC3339Nano, in)
	if err != nil {
		t.Fatalf("parsing the test's own input: %v", err)
	}
	wantStr := want.Local().Format("2006-01-02 15:04:05")
	if got != wantStr {
		t.Errorf("formatDate(%q) = %q, want %q", in, got, wantStr)
	}

	// An unparseable (e.g. empty) date is returned unchanged rather
	// than failing the whole command.
	if got := formatDate(""); got != "" {
		t.Errorf("formatDate(\"\") = %q, want \"\"", got)
	}
}

func TestParseArgs(t *testing.T) {
	i := func(n int) *int { return &n }

	cases := []struct {
		name                     string
		args                     []string
		acceptRev, acceptVerbose bool
		wantPositional           []string
		wantRev1, wantRev2       *int
		wantVerbose              bool
		wantErr                  bool
	}{
		{name: "no options", args: []string{"URL"}, acceptRev: true, wantPositional: []string{"URL"}},
		{name: "joined revision", args: []string{"-r5", "URL"}, acceptRev: true, wantPositional: []string{"URL"}, wantRev1: i(5)},
		{name: "joined revision range", args: []string{"-r5:9", "URL"}, acceptRev: true, wantPositional: []string{"URL"}, wantRev1: i(5), wantRev2: i(9)},
		{name: "separate revision", args: []string{"-r", "5", "URL"}, acceptRev: true, wantPositional: []string{"URL"}, wantRev1: i(5)},
		{name: "option after the positional argument", args: []string{"URL", "-r5"}, acceptRev: true, wantPositional: []string{"URL"}, wantRev1: i(5)},
		{name: "-v", args: []string{"-v", "URL"}, acceptVerbose: true, wantPositional: []string{"URL"}, wantVerbose: true},
		{name: "-r and -v together", args: []string{"-r5", "-v", "URL"}, acceptRev: true, acceptVerbose: true, wantPositional: []string{"URL"}, wantRev1: i(5), wantVerbose: true},
		{name: "-r on a subcommand that doesn't accept it", args: []string{"-r5", "URL"}, wantErr: true},
		{name: "-v on a subcommand that doesn't accept it", args: []string{"-v", "URL"}, wantErr: true},
		{name: "-r with no argument at all", args: []string{"-r"}, acceptRev: true, wantErr: true},
		{name: "-r with a non-numeric argument", args: []string{"-rabc", "URL"}, acceptRev: true, wantErr: true},
		{name: "-r with a negative revision", args: []string{"-r-1", "URL"}, acceptRev: true, wantErr: true},
		{name: "-r with a negative range start", args: []string{"-r-1:5", "URL"}, acceptRev: true, wantErr: true},
		{name: "-r with a negative range end", args: []string{"-r5:-1", "URL"}, acceptRev: true, wantErr: true},
		{name: "-r0 is a valid revision", args: []string{"-r0", "URL"}, acceptRev: true, wantPositional: []string{"URL"}, wantRev1: i(0)},
		{name: "unknown option", args: []string{"-x", "URL"}, acceptRev: true, acceptVerbose: true, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			positional, rev1, rev2, verbose, err := parseArgs(c.args, c.acceptRev, c.acceptVerbose)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseArgs(%v) = nil error, want one", c.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%v): %v", c.args, err)
			}
			if !reflect.DeepEqual(positional, c.wantPositional) {
				t.Errorf("positional = %v, want %v", positional, c.wantPositional)
			}
			if !revEqual(rev1, c.wantRev1) || !revEqual(rev2, c.wantRev2) {
				t.Errorf("rev1, rev2 = %v, %v, want %v, %v", derefOrNil(rev1), derefOrNil(rev2), derefOrNil(c.wantRev1), derefOrNil(c.wantRev2))
			}
			if verbose != c.wantVerbose {
				t.Errorf("verbose = %v, want %v", verbose, c.wantVerbose)
			}
		})
	}
}

func revEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func derefOrNil(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
