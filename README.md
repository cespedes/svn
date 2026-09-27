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
plain `svn checkout` (both sides -- see below) and, `svn.Server`-side only,
serving a single-revision `svn update`/`svn diff`/`svn switch`. It does not
implement commits, a mixed-revision `update`/`diff`/`switch` (and so nothing
that depends on that kind of thing, like `blame`), locking, or revision
properties. There is
no support for `svn://`'s raw TCP transport (only `file://` and `svn+ssh://`,
which both exec `svnserve -t` — see below) or for the HTTP-based (DAV)
protocol, and the only auth mechanisms implemented are `ANONYMOUS` and
`EXTERNAL` (no password/`CRAM-MD5` auth) — both client and server assume
either anonymous access or a transport that already authenticated the
connection (e.g. `svn+ssh://`'s SSH layer).

Concretely, this is what works and what doesn't, on each side (see
[PROTOCOL.md](PROTOCOL.md) for the exhaustive, command-by-command version
of this same table):

### Client (`svn.Client`, `go-svn`)

| `svn` subcommand equivalent | Works? | Notes |
| --- | --- | --- |
| `info` | ✅ | `GetLatestRev` + `Stat` |
| `cat` | ✅ | `GetFile` |
| `ls` | ✅ | `List`; only "immediates" depth has been exercised — recursive listing depends on the server understanding other `depth` values, which `List` merely passes through |
| `log` | ✅ | `Log`, including `-r`/revision ranges |
| `export` | ✅ | recursive `List` + `GetFile` walk (`go-svn export <repo> [localdir]`), no report/editor exchange needed since it doesn't create a working copy |
| `checkout` | ✅ for a plain checkout | `Client.Checkout` drives the same report/editor exchange the server side uses, reporting "I have nothing" and then parsing the resulting Editor Command Set sequence to create files/directories under a destination directory -- no ".svn" working-copy metadata, so a real `svn update`/`status` can't later run against the result (matching `export`'s own simplification). To check out a repository subdirectory, `Connect`/`NewClient` to that subdirectory's own URL directly; `Checkout` has no separate "path within the repository" parameter (see its own doc comment for why) |
| `update` / `switch` | ❌ | needs a real (non-start-empty) report describing an existing working copy, which `Checkout`'s own report/editor plumbing doesn't build yet |
| `diff` / `blame` (`praise`) | ❌ | needs `update`/`get-file-revs`, neither implemented |
| `propget` / `proplist` on a file | partial | `GetFile`'s properties are returned if requested; there's no dedicated single-property call |
| `propget` / `proplist` on a directory | ❌ | |
| `lock` / `unlock` | ❌ | |
| `commit` / `add` / `delete` / `mkdir` / `import` | ❌ | no write support at all |
| `mergeinfo` | ❌ | |

### Server (`svn.Server`)

| Command a client sends | Handled? | Notes |
| --- | --- | --- |
| `get-latest-rev`, `stat`, `check-path`, `list`, `get-file`, `log` | ✅ | one callback field each; a `nil` field replies "unimplemented" |
| `get-iprops` | ✅ | always reports no inherited properties (neither modeled anywhere in this package); not gated behind a callback field, since a real client needs an answer to it to complete even a plain checkout below the repository root |
| `get-dir` | ✅ | a thin wrapper around `Server.List`; superseded by `list` for a modern client's normal directory browsing, but `svn diff` still falls back to it to enumerate a deleted directory's former contents |
| `reparent` | ✅ | purely informational, like `set-path`; a real client commonly reparents an existing session (rather than opening a new connection) while preparing a `switch` -- see the next row |
| `set-path`, `update`, `diff`, `switch`, `finish-report` | ✅ for a checkout or a single-revision update/diff/switch | `Serve` accumulates every `set-path` into a `[]ReportedPath` and hands it to `FinishReport`. `IsPlainCheckout` + `Server.CheckoutEdit` handle a plain checkout (the client has nothing yet); `IsSingleRevisionUpdate` + `Server.UpdateEdit` handle a real `update` or `diff` for a working copy that isn't "mixed revision" (every subtree at the same revision), walking both the client's revision and the target revision via `List`/`GetFile` and describing only what changed -- new/removed/modified nodes -- skipping an unmodified file entirely (a matching `CreatedRev` is conclusive proof of that, since it's the same path at two revisions); `Server.SwitchEdit` handles a `switch` the same way, but diffing two different repository locations (corresponding to each other by name, not by shared history) rather than two revisions of the same one -- and, since a matching `CreatedRev` proves nothing across two different paths (two unrelated files can share one by pure coincidence, e.g. both added in the same commit), it never skips a same-named file as unchanged, always resending its content instead. A `diff` against a bare repository URL (no local working copy) additionally needs `get-dir` (above). `UpdateEdit`/`SwitchEdit`'s `target` parameter also handles a client naming a single nested file or subdirectory (`svn update path/to/file`, `svn diff path/to/file`), not just a whole working copy: every path segment strictly between the report's own root and the target is walked but never itself described in the editor sequence, since a real client computes the target's own local path by joining every directory name it receives and expects the target's parent to coincide with the edit's root regardless of how many real path segments separate them (see `UpdateEdit`'s doc comment, and `RepoRelativePath` for how a `diff`/`switch` callback recovers the target's true path when its session is anchored above the target's own parent). A mixed-revision working copy (part of it pinned to an older revision) isn't recognized by any of these |
| everything else (`delete-path`/`link-path`, most of the editor command set beyond what a checkout/update/diff/switch needs, `commit`, locking, revision properties, `get-mergeinfo`, `get-file-revs`, `replay`, ...) | ❌ | not handled: replies "Unknown command" — see [PROTOCOL.md](PROTOCOL.md) for the full list |

In practice: a real `svn info`/`ls`/`cat`/`log`/`checkout`/`update`/`diff`/`switch`
against a `svn.Server` implementation works (confirmed against a real `svn`
client — see [Development](#development)), as long as the working copy
isn't "mixed revision"; `svn commit` does not. In the other direction,
`svn.Client.Checkout` works against a real `svnserve` the same way (confirmed
in `svn_integration_test.go`).

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
go-svn cat svn+ssh://example.com/repo/trunk/README
go-svn -v ls svn+ssh://example.com/repo/trunk
go-svn -r 100:200 log svn+ssh://example.com/repo
go-svn export svn+ssh://example.com/repo/trunk
```

Usage: `go-svn [-v] [-r revision[:revision2]] <subcommand> <repo>`.
Subcommands: `info`, `cat`, `ls`, `log`, `export <repo> [localdir]`. `-r rev`
or `-r rev1:rev2` selects a revision or revision range where the subcommand
supports it, and `-v` asks for more detail (`ls`, `log`). `export` writes a
clean copy of `repo` (no version-control metadata) to `localdir`, or to a
directory named after `repo`'s own last path segment if `localdir` is
omitted.

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
