# svn

[![Go reference](https://pkg.go.dev/badge/github.com/cespedes/svn)](https://pkg.go.dev/github.com/cespedes/svn)

This package provides client and server implementations of the
[SVN protocol (ra_svn)](https://svn.apache.org/repos/asf/subversion/trunk/subversion/libsvn_ra_svn/protocol)
in Go: the wire protocol `svnserve` and `svn+ssh://` URLs use, as opposed to
the HTTP-based `http://`/`https://` (DAV) protocol.

This is a work in progress, and it is in a very early stage. On the client
side, connecting, `get-latest-rev`, `stat`, `list`, `get-file` and `log`
already work; `update` and the report commands it depends on are not
implemented yet. The server side lets you plug in handlers for the same set
of read commands; there is no support yet for commits or for driving an
update/report exchange.

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

A `svn.Client` serializes one request at a time over a single connection and
has no locking of its own, so an `FS` is only as safe for concurrent use as
the `Client` behind it -- share one per goroutine, or serialize access with
a mutex or a connection pool, rather than sharing one `Client` across
concurrent requests directly.

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

## License

[MIT](LICENSE)
