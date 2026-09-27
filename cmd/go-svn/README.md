# go-svn

`go-svn` is a small `svn`-like command-line client built on top of
[`github.com/cespedes/svn`](https://github.com/cespedes/svn), the SVN
(Subversion) wire protocol implementation this directory lives in. It only
covers read-only operations -- there is no write support (`commit`, `add`,
...) anywhere in the underlying package.

## Installation

```sh
go install github.com/cespedes/svn/cmd/go-svn@latest
```

## Usage

```
go-svn [-v] [-r revision[:revision2]] <subcommand> <repo>
```

| Subcommand | Notes |
| --- | --- |
| `info` | repository/node metadata (URL, UUID, revision, last author, ...); does not accept `-v` or a revision range |
| `cat` | prints a file's content at `-r revision` (default: latest) |
| `ls` | lists a directory's direct children; `-v` adds revision/author/size/date columns |
| `log` | shows log messages; `-r rev1:rev2` selects a revision range, `-v` also lists each entry's changed paths |
| `export <repo> [localdir]` | writes a clean copy of `repo` (no version-control metadata) to `localdir`, or to a directory named after `repo`'s own last path segment if `localdir` is omitted |

`-r rev` or `-r rev1:rev2` selects a revision or revision range, where the
subcommand supports it.

`repo` is a repository URL: `file://` and `svn+ssh://` are supported (see
the main package's own
[README](https://github.com/cespedes/svn#readme) for how each is
connected to), optionally pointing below the repository root (e.g.
`svn+ssh://example.com/repo/trunk`) to operate on just that subtree.

## Examples

```sh
go-svn info svn+ssh://example.com/repo
go-svn cat svn+ssh://example.com/repo/trunk/README
go-svn -v ls svn+ssh://example.com/repo/trunk
go-svn -r 100:200 log svn+ssh://example.com/repo
go-svn export svn+ssh://example.com/repo/trunk
```
