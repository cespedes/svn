# go-svn

`go-svn` is a small `svn`-like command-line client built on top of
[`github.com/cespedes/svn`](https://github.com/cespedes/svn), the SVN
(Subversion) wire protocol implementation this directory lives in.
`checkout`/`update`/`commit` are the only subcommands that touch a local
filesystem (the only place in this codebase that does); `commit` is also
the only one that changes the repository itself, by comparing a working
copy `checkout` produced against the repository and sending whatever
differs -- there is no separate `add`/`delete`/`mkdir`/`import`, unlike a
real `svn`: a file dropped into (or removed from) a checked-out
directory by any means is committed as an addition (or a deletion) the
next time `commit` runs, with no staged "schedule" step in between.

## Installation

```sh
go install github.com/cespedes/svn/cmd/go-svn@latest
```

## Usage

```
go-svn <subcommand> [-r revision[:revision2]] [-v] <repo>
```

Options go *after* the subcommand name, the same way a real `svn`'s own
command-line works (`go-svn cat -r100 URL`, not `go-svn -r 100 cat URL`),
and only the subcommands that actually use a given option accept it at all
-- passing one to a subcommand that doesn't is an error, not silently
ignored.

| Subcommand | Notes |
| --- | --- |
| `info` | repository/node metadata (URL, UUID, revision, last author, ...); does not accept `-v` or a revision range |
| `cat` | prints a file's content at `-r revision` (default: latest) |
| `ls` | lists a directory's direct children; `-v` adds revision/author/size/date columns |
| `log` | shows log messages; `-r rev1:rev2` selects a revision range, `-v` also lists each entry's changed paths |
| `export <repo> [localdir]` | writes a clean copy of `repo` (no version-control metadata) to `localdir`, or to a directory named after `repo`'s own last path segment if `localdir` is omitted |
| `checkout <repo> [localdir]` | like `export`, but driving a real report/editor exchange instead of a one-shot recursive walk, so `update` can later bring the result forward touching only what changed |
| `update [localdir]` | brings a directory `checkout` produced up to a newer revision (default: latest) in place; `localdir` defaults to the current directory, like a real `svn update` |
| `commit -m message [localdir]` | sends every local change under `localdir` (added, removed or modified since it was last checked out or updated) back to the repository as a new revision; `localdir` defaults to the current directory, like a real `svn commit` |

`-r rev` or `-r rev1:rev2` selects a revision or revision range, where the
subcommand supports it, either joined (`-r100`, `-r100:200`) or as a
separate argument (`-r 100`, `-r 100:200`). `-m message` (`commit` only)
takes the commit message the same way, joined (`-mfix bug`) or separate
(`-m "fix bug"`).

`repo` is a repository URL: `file://` and `svn+ssh://` are supported (see
the main package's own
[README](https://github.com/cespedes/svn#readme) for how each is
connected to), optionally pointing below the repository root (e.g.
`svn+ssh://example.com/repo/trunk`) to operate on just that subtree.

`checkout`/`update` write the same kind of plain, unversioned tree
`export` does -- no `.svn` working-copy metadata -- but remember which
repository URL and revision a directory holds in a small `.go-svn-checkout`
sidecar file, so `update`/`commit` need no URL argument.

## Examples

```sh
go-svn info svn+ssh://example.com/repo
go-svn cat -r100 svn+ssh://example.com/repo/trunk/README
go-svn ls -v svn+ssh://example.com/repo/trunk
go-svn log -r100:200 svn+ssh://example.com/repo
go-svn export svn+ssh://example.com/repo/trunk
go-svn checkout svn+ssh://example.com/repo/trunk
go-svn update trunk
go-svn commit -m "fix bug" trunk

# from inside trunk itself, localdir can be omitted:
go-svn update
go-svn commit -m "fix bug"
```
