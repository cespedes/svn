# svn

[![Go reference](https://pkg.go.dev/badge/github.com/cespedes/svn)](https://pkg.go.dev/github.com/cespedes/svn)

This package provides client and server implementations of the
[SVN protocol (ra_svn)](https://svn.apache.org/repos/asf/subversion/trunk/subversion/libsvn_ra_svn/protocol)
in Go: the wire protocol `svnserve` and `svn+ssh://` URLs use, as opposed to
the HTTP-based `http://`/`https://` (DAV) protocol.

This is a work in progress, and it is in a very early stage: it only covers
a subset of ra_svn, described in detail below.

## Protocol coverage

This package implements the **read-only, unversioned-property, non-locking**
part of ra_svn: browsing and reading a repository at a given revision, plus a
plain `svn checkout` and a single-revision `svn update`/`svn diff` (all three
on both sides -- see below) and, `svn.Server`-side only, serving a
single-revision `svn switch` too. `svn.Client`-side, it also implements
`commit`, driving the client's own end of the Editor Command Set exchange --
but only `svn.Client`-side: a `svn.Server` implementation still cannot
*receive* a commit from a real client. It does not implement a
mixed-revision `update`/`diff`/`switch` (and so nothing that depends on that
kind of thing, like `blame`), locking, or revision
properties. There is
no support for `svn://`'s raw TCP transport (only `file://` and `svn+ssh://`,
which both exec `svnserve -t` — see below) or for the HTTP-based (DAV)
protocol, and the only auth mechanisms implemented are `ANONYMOUS` and
`EXTERNAL` (no password/`CRAM-MD5` auth) — both client and server assume
either anonymous access or a transport that already authenticated the
connection (e.g. `svn+ssh://`'s SSH layer).

Concretely, this is what works and what doesn't, on each side (see
[docs/protocol.md](docs/protocol.md) for the exhaustive, command-by-command version
of this same table, and [docs/architecture.md](docs/architecture.md) for
how the Editor Command Set exchange behind `checkout`/`update`/`diff`/
`switch`/`commit` is actually implemented on each side):

### Client (`svn.Client`, `go-svn`)

| `svn` subcommand equivalent | Works? | Notes |
| --- | --- | --- |
| `info` | ✅ | `GetLatestRev` + `Stat` |
| `cat` | ✅ | `GetFile` |
| `ls` | ✅ | `List`; only "immediates" depth has been exercised — recursive listing depends on the server understanding other `depth` values, which `List` merely passes through |
| `log` | ✅ | `Log`, including `-r`/revision ranges |
| `export` | ✅ | recursive `List` + `GetFile` walk (`go-svn export <repo> [localdir]`), no report/editor exchange needed since it doesn't create a working copy |
| `checkout` | ✅ for a plain checkout | `Client.Checkout` drives the same report/editor exchange the server side uses, calling a caller-supplied [`Editor`](#checking-out-updating-and-diffing-sveditor)'s own fields for each node described. To check out a repository subdirectory, `Connect`/`NewClient` to that subdirectory's own URL directly |
| `update` | ✅ for a single-revision update | `Client.Update` calls the same `Editor`'s fields for only what actually changed since `fromRev`. A mixed-revision working copy isn't supported |
| `switch` | ❌ | needs target-selection logic (diffing against a *different* repository location), which `Client.Update`'s own plumbing doesn't build yet |
| `diff` | ✅ for a single-revision comparison against this Client's own history | `Client.Diff` reuses `Update`'s own `Editor`; comparing two different repository locations (`svn diff OLD-URL NEW-URL`) isn't supported |
| `blame` (`praise`) | ❌ | needs `get-file-revs`, not implemented |
| `propget` / `proplist` on a file | partial | `GetFile`'s properties are returned if requested; there's no dedicated single-property call |
| `propget` / `proplist` on a directory | ❌ | |
| `lock` / `unlock` | ❌ | |
| `commit` / `add` / `delete` / `mkdir` / `import` | ✅ | `Client.Commit` sends a caller-built Editor Command Set (`EditorWriter`, the same builder used on the read side); `cmd/go-svn commit` builds it by comparing a working copy against the repository (see below) -- there's no staged add/remove step, unlike a real `svn add`/`svn rm` |
| `mergeinfo` | ❌ | |

### Server (`svn.Server`)

| Command a client sends | Handled? | Notes |
| --- | --- | --- |
| `get-latest-rev`, `stat`, `check-path`, `list`, `get-file`, `log` | ✅ | one callback field each; a `nil` field replies "unimplemented" |
| `get-iprops` | ✅ | always reports no inherited properties (neither modeled anywhere in this package); not gated behind a callback field, since a real client needs an answer to it to complete even a plain checkout below the repository root |
| `get-dir` | ✅ | a thin wrapper around `Server.List`; superseded by `list` for a modern client's normal directory browsing, but `svn diff` still falls back to it to enumerate a deleted directory's former contents |
| `reparent` | ✅ | purely informational, like `set-path`; a real client commonly reparents an existing session (rather than opening a new connection) while preparing a `switch` -- see the next row |
| `set-path`, `update`, `diff`, `switch`, `finish-report` | ✅ for a checkout or a single-revision update/diff/switch | `IsPlainCheckout`/`Server.CheckoutEdit` handle a plain checkout; `IsSingleRevisionUpdate`/`Server.UpdateEdit`/`Server.SwitchEdit` handle a real `update`/`diff`/`switch` for a working copy that isn't "mixed revision", diffing the client's revision against the target (or a different repository location, for `switch`) and describing only what changed. See [docs/architecture.md](docs/architecture.md) for how this works and its wire-level gotchas |
| everything else (`delete-path`/`link-path`, most of the editor command set beyond what a checkout/update/diff/switch needs, `commit`, locking, revision properties, `get-mergeinfo`, `get-file-revs`, `replay`, ...) | ❌ | not handled: replies "Unknown command" — see [docs/protocol.md](docs/protocol.md) for the full list |

In practice: a real `svn info`/`ls`/`cat`/`log`/`checkout`/`update`/`diff`/`switch`
against a `svn.Server` implementation works (confirmed against a real `svn`
client — see [Development](#development)), as long as the working copy
isn't "mixed revision"; `svn commit` does not (there is no `Server.Commit`
or equivalent). In the other direction, `svn.Client.Checkout`/`Update`/
`Diff`/`Commit` work against a real `svnserve` the same way (confirmed in
`svn_integration_test.go`). See [docs/architecture.md](docs/architecture.md)
for the wire-level gotchas this interop testing found along the way.

## Installation

```sh
go get github.com/cespedes/svn
```

## Using the client

```go
c, err := svn.Connect("svn+ssh://example.com/repo")
if err != nil {
	log.Fatal(err)
}

rev, err := c.GetLatestRev()
if err != nil {
	log.Fatal(err)
}
fmt.Println("latest revision:", rev)
```

`Connect` currently supports `file://` and `svn+ssh://` URLs: it runs
`svnserve -t` locally (for `file://`) or over `ssh` (for `svn+ssh://`) and
speaks the protocol over its standard input/output. See
[`examples/client`](examples/client) for a more complete example, covering
`Stat`, `List` and `GetFile`.

If the underlying connection breaks (the ssh tunnel drops, the local
`svnserve` subprocess dies, ...), a `Connect`-created `Client` reconnects
transparently and retries the call once -- safe to do because every RPC is
read-only. A `Client` made with `NewClient` (over a caller-supplied
connection) can't do this, since it has no way to reopen that connection
itself.

`c.SetDebug(os.Stderr)` (or any other `io.Writer`) makes the `Client` log
every message it sends and receives from then on, prefixed `"> "`/`"< "`
(like `curl -v`); `nil` stops it. Since `Connect`/`NewClient` already
complete the initial handshake before returning, `SetDebug` can never log
it; set the package-level `svn.DefaultDebug` beforehand instead to also
capture that. `go-svn -d <subcommand> ...` (the `-d` flag goes *before*
the subcommand name, since it isn't specific to any one of them) uses
`DefaultDebug` for exactly this reason, to show the whole exchange
including the handshake.

## Checking out, updating and diffing: svn.Editor

`Client.Checkout`/`Client.Update`/`Client.Diff` each drive a real "svn
checkout"/"svn update"/"svn diff" report/editor exchange, but -- like every
other part of this package -- never touch a filesystem, a database, or
anything else on their own: what each node in the resulting tree actually
*means* is entirely up to a caller-supplied `svn.Editor`, a struct of
callback fields (one per Editor Command Set command) mirroring `svn.Server`'s
own style. All three methods share the exact same `Editor`: `Diff` is what
`Update` would be if, instead of overwriting `Editor.OpenFile`'s own
"before" content with `Editor.CloseFile`'s "after" content, the caller
printed a diff between the two instead:

```go
var written int
editor := svn.Editor{
	AddFile: func(path string, copyFrom *svn.EditorCopyFrom) error {
		fmt.Println("new file:", path)
		return nil
	},
	CloseFile: func(path string, content []byte) error {
		written += len(content)
		return nil
	},
}
rev, err := c.Checkout(nil, editor) // nil: the latest revision
```

See [`examples/checkout`](examples/checkout) for a small runnable version
of the snippet above, and [`cmd/go-svn`](cmd/go-svn)'s own `checkout`/
`update` subcommands for a complete, filesystem-backed `Editor` (creating
real files/directories under a destination directory) built purely on
this API -- the library deliberately stops short of providing one itself.

## Using the server

`svn.Server` drives the server side of the protocol handshake and dispatches
incoming commands to the callback fields you set; any command left as `nil`
replies "unimplemented" to the client.

```go
var server svn.Server
server.GetLatestRev = func() (int, error) {
	return 1000, nil
}
server.Stat = func(path string, rev *uint) (svn.Dirent, error) {
	return svn.Dirent{Kind: "dir"}, nil
}
err := server.Serve(os.Stdin, os.Stdout)
```

See [`examples/server`](examples/server) for a runnable version of this.
Setting `server.Debug = os.Stderr` (or any other `io.Writer`) logs every
message `Serve` sends and receives, the same way `Client.SetDebug` does
-- including the initial greeting, since `Serve` writes it itself rather
than receiving an already-handshaken connection.

## svnfs: an io/fs.FS adapter

The [`svnfs`](svnfs) subpackage adapts a `*svn.Client` into a read-only
[`io/fs.FS`](https://pkg.go.dev/io/fs#FS), so standard-library tools
(`http.FileServerFS`, `fs.WalkDir`, `fs.Glob`, ...) can browse and read files
straight out of an SVN repository:

```go
c, err := svn.Connect("svn+ssh://example.com/repo")
if err != nil {
	log.Fatal(err)
}
fsys := svnfs.New(c, nil) // nil rev: always the latest revision

http.Handle("/", http.FileServerFS(fsys))
```

A single `svn.Client` (and any `FS` built on it) is safe to share across
goroutines: it locks around each command internally. That buys safety, not
parallelism, though -- the protocol has no way to pipeline or multiplex
commands over one connection, so concurrent requests still run one at a
time, queued behind each other. For real concurrency, use a pool of
`Client`s (one connection each) instead of sharing one.

## Command-line client: go-svn

[`cmd/go-svn`](cmd/go-svn) is a small `svn`-like command-line client built
on top of this package:

```sh
go install github.com/cespedes/svn/cmd/go-svn@latest

go-svn info svn+ssh://example.com/repo
go-svn cat -r100 svn+ssh://example.com/repo/trunk/README
go-svn ls -v svn+ssh://example.com/repo/trunk
go-svn log -r100:200 svn+ssh://example.com/repo
go-svn export svn+ssh://example.com/repo/trunk
go-svn checkout svn+ssh://example.com/repo/trunk
go-svn update trunk
go-svn commit -m "fix bug" trunk
```

Usage: `go-svn <subcommand> [-r revision[:revision2]] [-v] <repo>`, matching
a real `svn`'s own command-line shape: `-r`/`-v`/`-m` go *after* the
subcommand name, not before it (`go-svn cat -r100 URL`, not `go-svn -r 100
cat URL`), and only the subcommands that actually use a given option accept
it at all. Subcommands: `info`, `cat`, `ls`, `log`, `export <repo>
[localdir]`, `checkout <repo> [localdir]`, `update [localdir]`, `commit -m
message [localdir]`. `checkout` writes a plain, unversioned tree (no `.svn`
working-copy metadata) by driving a real report/editor exchange (see
`svn.Editor` above); `update`/`commit` bring it forward or send local
changes back, touching only what changed, and default `localdir` to the
current directory if omitted, like a real `svn`. See
[`cmd/go-svn`](cmd/go-svn)'s own README for the full option/subcommand
reference.

## Other examples

[`examples`](examples) also has small, focused programs for the lower-level
pieces of the package: tokenizing (`read-tokens`), parsing into `Item`s
(`read-items`), and `Marshal` (`marshal`).

## Development

```sh
go build ./...
go test ./...
```

If `svnadmin`, `svn` and `svnserve` are installed (e.g. Debian/Ubuntu's
`subversion` package), `go test ./...` also runs a handful of integration
tests against a real, temporary repository; they're skipped cleanly
otherwise.

## License

[MIT](LICENSE)
