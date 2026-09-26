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
part of ra_svn: browsing and reading a repository at a given revision. It
does not implement commits, checkout/update (and so nothing that depends on
it, like `diff` or `blame`), locking, or revision properties. There is no
support for `svn://`'s raw TCP transport (only `file://` and `svn+ssh://`,
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
| `checkout` / `update` / `switch` | ❌ | needs the report/editor exchange, not implemented on the client side |
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
| `set-path`, `update` | callbacks invoked, but incomplete | called with the parsed arguments, but there's no way to report back a result: driving the actual update requires the report/editor command sequence below, which isn't implemented |
| everything else (the rest of the report/editor command sets, `commit`, locking, revision properties, `get-mergeinfo`, `get-file-revs`, `replay`, ...) | ❌ | not handled: replies "Unknown command" — see [PROTOCOL.md](PROTOCOL.md) for the full list |

In practice: a real `svn info`/`ls`/`cat`/`log` against a `svn.Server`
implementation works (confirmed against a real `svn` client — see
[Development](#development)); `svn checkout`/`update`/`commit` do not.

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

`cmd/go-svn` is a small `svn`-like command-line client built on top of this
package:

```sh
go install github.com/cespedes/svn/cmd/go-svn@latest

go-svn info svn+ssh://example.com/repo
go-svn cat svn+ssh://example.com/repo/trunk/README
go-svn -v ls svn+ssh://example.com/repo/trunk
go-svn -r 100:200 log svn+ssh://example.com/repo
```

Usage: `go-svn [-v] [-r revision[:revision2]] <subcommand> <repo>`.
Subcommands: `info`, `cat`, `ls`, `log`. `-r rev` or `-r rev1:rev2` selects a
revision or revision range where the subcommand supports it, and `-v` asks
for more detail (`ls`, `log`).

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
