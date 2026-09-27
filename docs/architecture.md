# Architecture

This document describes how the package is put together internally: the
layered stack from raw bytes up to the `Client`/`Server` APIs, the Editor
Command Set mechanism `checkout`/`update`/`diff`/`switch`/`commit` all
reduce to, and the wire-level conventions that aren't obvious from the
protocol's own spec text. For *what* is implemented, see the
[Protocol coverage](../README.md#protocol-coverage) section of the README
and [protocol.md](protocol.md); for *how to use* the package, see the
README's own usage sections. This document is about *why* the code looks
the way it does.

## The layered stack

Reading the source in this order is the fastest way to understand the
protocol stack:

1. **Tokenizer** (`token.go`) turns a byte stream into the protocol's 5
   wire tokens: word, number, string, `(`, `)`.
2. **Itemizer** (`item.go`) groups tokens into `Item`, a recursive tree
   (`WordType` / `NumberType` / `StringType` / `ListType`) that mirrors the
   protocol's own data model — this is its AST.
3. **Marshal/Unmarshal** (`marshal.go`) converts between Go values
   (structs, slices, strings, `[]byte`, bools, ints, pointers) and `Item`
   via reflection, similar in spirit to `encoding/json`. A struct's
   exported fields marshal positionally, in declaration order — this is a
   positional protocol, not a keyed one, so there are no field tags for
   names. `[]byte` marshals as a length-prefixed `StringType`; a plain Go
   `string` marshals as a bare `WordType` ("word"), which may only hold
   alphanumerics and `-` on the wire. This distinction matters more than
   it looks: see "Wire-level conventions" below. A nil pointer marshals to
   nothing (dropped from the enclosing list); on unmarshal, a `[ ... ]`
   (the protocol's own optional/0-or-1-element list shape) commonly maps
   to a Go pointer.
4. **conn** (`conn.go`) is the low-level read/write connection: it wraps
   an `io.Reader`/`io.Writer`, marshals outgoing values into items, and
   reads and unmarshals incoming ones. `ReadResponse` additionally
   unwraps the protocol's own `( success ( ... ) )` / `( failure ( ... ) )`
   envelope (`response.go`), turning a `failure` into a Go `error`. Not
   every response uses this envelope — `Client.Commit`'s own deferred
   response is a notable exception (see below). Every read and write
   already has the parsed `Item` in hand at exactly one point each
   (`Write`, right after `Marshal`; `Read`, right after the `Itemizer`
   returns it) — an optional `debug io.Writer` field logs it there,
   prefixed `"> "`/`"< "`, giving `Client.SetDebug`/`Server.Debug` a full
   wire trace for free, at the `Item` level rather than raw bytes (whose
   chunk boundaries across a pipe/socket read are misleading — the same
   reason every wire capture done while building this package moved to
   logging whole items instead). `Client.SetDebug` can only ever be
   called once `Connect`/`NewClient` already returned, after their own
   handshake is done, so it can't log that; the package-level
   `DefaultDebug` variable, read into a new `Client`'s own `conn` before
   the handshake runs (mirroring `net/http.DefaultTransport`'s own
   set-once-at-startup convention), is what covers it instead.
5. **Client** (`client.go`) implements the client side of the handshake
   (greeting, version/capability negotiation, `EXTERNAL`/anonymous auth)
   and individual RPCs on top of `conn`. `Connect(address)` supports
   `file://` and `svn+ssh://` URLs by exec'ing `svnserve -t` (locally, or
   over `ssh`) and speaking the protocol over its stdin/stdout; there is
   no native TCP client for `svn://`. `NewClient(r, w, address)` runs the
   same handshake over a caller-supplied connection instead. A `Client` is
   safe to share across goroutines (an internal mutex serializes each
   call), but the protocol has no pipelining, so this buys safety, not
   parallelism. A `Connect`-created `Client` also reconnects and retries
   once on a connection error, for any RPC that's safe to retry because
   it's read-only; `Checkout`/`Update`/`Diff`/`Commit` deliberately don't,
   since resending a partial exchange risks duplicate callback
   invocations or a duplicate commit.
6. **Server** (`server.go`) is the mirror image: `Server.Serve(r, w)`
   drives the server side of the same handshake, then loops reading
   commands and dispatching to caller-supplied callback fields. A `nil`
   callback replies "unimplemented"; a command with no case in the switch
   replies "Unknown command". `Serve` returns `io.EOF` once the connection
   ends cleanly, any other error otherwise.
7. **svnfs** (`svnfs/svnfs.go`) adapts a `*Client` into a read-only
   `io/fs.FS`, built entirely on the public `Client` API.
8. **svndiff** (`svndiff.go`) encodes/decodes the `apply-textdelta`
   payload format. `EncodeSvndiff` always produces a single, sourceless
   window (a full replacement, never an incremental delta); `decodeSvndiff`
   handles the general case, including a real svnserve's own incremental
   deltas against a source, which `Client.Update` needs when applying a
   modified file's content.
9. **Editor Command Set support** (`editor.go`, `checkout.go`,
   `update.go`, `client_editor.go`, `client_checkout.go`,
   `client_update.go`, `client_diff.go`, `client_commit.go`) is the
   largest single piece of the package, described in its own section
   below.

## The Editor Command Set

The Editor Command Set describes a tree of changes, one command per node
touched (`open-root`, `add-dir`/`open-dir`, `add-file`/`open-file`,
`apply-textdelta`, `close-file`/`close-dir`, `delete-entry`, ...). It's
the shape a `checkout`/`update`/`diff`/`switch` response takes (sent
server → client, after the client's own `finish-report`), and, in the
opposite direction, the shape a `commit` sends (client → server). Two
conventions hold in both directions and are easy to get wrong without a
real client/server to check against:

- A `path` argument is always the **full path from the edit's root**
  (e.g. `"trunk/main.go"`), never just the child's own name relative to
  its immediate parent.
- **Tokens** (the opaque strings a command uses to refer to a
  previously-opened node, e.g. `"d0"`, `"c1"`) are entirely an
  implementation detail: any unique string works, and neither side needs
  to agree on a scheme beyond that.

### Writing one: `EditorWriter`

`EditorWriter` (`editor.go`) builds a Editor Command Set sequence as a
`[]Item`, via one typed method per command instead of hand-built `Item`s.
It enforces strict LIFO open/close nesting (an `Add*`/`Open*` call's
matching `Close*` must be the next one at that nesting level), which
both matches how a tree of changes is naturally described by a recursive
walk and catches a caller forgetting to close a node.

It's used in two, unrelated places:
- **`Server`-side**, to answer `finish-report`: `IsPlainCheckout` +
  `Server.CheckoutEdit` (`checkout.go`) handle a plain checkout by walking
  the target revision and describing every node as newly added;
  `IsSingleRevisionUpdate` + `Server.UpdateEdit` (`update.go`) handle a
  real `update`/`diff` by diffing two revisions of the same tree and
  describing only what changed; `Server.SwitchEdit` (also `update.go`)
  generalizes the same diffing to two *different* repository paths
  (`svn switch`) instead of two revisions of the same one. All three stop
  short of a "mixed revision" working copy (part of it pinned to an older
  revision), which isn't recognized or supported.
- **`Client`-side**, by `Client.Commit` (`client_commit.go`): the caller
  builds the whole tree of local changes with `EditorWriter` first, then
  hands the resulting `[]Item` to `Commit`, which streams it to the
  server. `cmd/go-svn commit` is the real, filesystem-reading use of this:
  it compares a checked-out directory against the repository at its
  recorded base revision and builds the `[]Item` from whatever differs —
  deliberately simpler than a real `svn commit`, with no staged
  add/remove/schedule step.

### Reading one: `Editor` and `driveEditor`

`Editor` (`client_editor.go`) is a struct of callback fields, one per
Editor Command Set command, mirroring `Server`'s own callback-field style;
`driveEditor` parses an incoming sequence and calls one field per command.
It's a plain function taking a `*conn` directly (not a `*Client` method),
since parsing is exactly as direction-agnostic as `EditorWriter`'s own
writing is -- deliberately kept ready to read a client-driven commit
server-side too, not just what `Client.Checkout`/`Update`/`Diff` share
today (`client_editor.go`'s `reportAndApply` drives the wire exchange
itself; `driveEditor` is the reader, stopping once it acks
`close-edit`/`abort-edit`, leaving whatever deferred response the
command that started the exchange still owes to its own caller).
None of `Client`, `Editor`, or `driveEditor` ever touches a
filesystem, a database, or any other storage on its own — this package
exists to *build* SVN clients/servers out of, not to *be* a high-level,
storage-backed client itself. `cmd/go-svn`'s `diskEditor` is the concrete,
filesystem-writing `Editor` its own `checkout`/`update` subcommands use;
`svn.Editor`'s doc comment and the README's own usage section point here
for anyone building a different backing store (an in-memory tree, a
different version-control system, ...).

A few of `Editor`'s fields carry a subtlety worth remembering:
`Editor.OpenFile` (called for a file the report claims the caller already
has, never for a brand new one) must return that file's own current
content, since a real svnserve may describe the new content as a genuine
incremental delta *against that base* rather than a full replacement —
`decodeSvndiff` needs the real source to apply it correctly.

### Server-side status

A real `svn checkout`, single-revision `svn update`/`svn diff`, and
single-revision `svn switch` all work end to end against a `svn.Server`
implementation. There is currently **no way for a `Server` to receive a
commit**: nothing in this package's `Server` API models accepting an
incoming, client-driven Editor Command Set, deciding what "committing" it
means against a backing store, or reporting back a new revision. Building
this would need:

- ~~Generalizing `driveEditor`/`Editor` to read from a `*conn` directly~~
  **done**: `driveEditor` is now a package-level function taking a
  `*conn`, not a `*Client` method, and stops right after acking
  `close-edit`/`abort-edit` rather than also reading `finish-report`'s
  own deferred response itself -- that response's shape is specific to
  whichever Main Command Set command started the exchange
  (`finish-report` for an update/diff/switch, `commit` for a commit), so
  reading (or, server-side, writing) it is the caller's own job now
  (`reportAndApply` does the reading, client-side). The wire format is
  the same regardless of direction, so the same reader and callback
  shape already serves a client parsing a server-driven edit, and is
  ready to serve a server parsing a client-driven one the same way.
- ~~New `Server` callbacks mirroring the existing `Update` + `FinishReport`
  split~~ **done**: `Server.Commit(logMessage string, revprops []PropList)
  (Editor, error)`, invoked when `"commit"` arrives (to let an
  implementation open its own transaction and return the `Editor` to
  drive), and `Server.FinishCommit() (CommitInfo, error)`, invoked once
  the client's `close-edit` is received, to report back the new revision.
  Neither is wired into `Serve`'s own dispatch switch yet -- there's
  still no `"commit"` case at all, so this alone changes nothing a real
  client can observe.
- Deciding where a commit's author identity comes from: `Server` today
  only distinguishes `ANONYMOUS`/`EXTERNAL` at the transport level, with
  no existing per-connection "who is this" concept.

## Wire-level conventions

The upstream protocol doc's own grammar is occasionally ambiguous or
under-specified about exact nesting depth or field encoding. The shapes
below were fixed to match a **real svnserve's actual bytes**, confirmed by
wire capture and regressed with tests that replay literal captured wire
text — prefer testing against a real `svnserve`/`svn` client (see
"Testing", below) over re-deriving a shape from the spec text alone if you
touch any of these again:

- `stat`'s response entry is nested **two** list levels deep:
  `( ( ( kind size has-props created-rev [date] [author] ) ) )`, not one.
- `open-dir`/`open-file`'s `rev` and `delete-entry`'s `rev` are wrapped as
  `[ rev:number ]` on the wire, despite the protocol grammar showing a
  bare, required `rev:number` for both.
- `list`'s returned `Dirent.Path` comes back as `"/" + the full,
  repository-root-relative path` (never a bare child name, and never
  relative to whatever directory was queried), and the response always
  includes the queried directory itself as one of its own "children". A
  real `svn` client segfaults, not just errors, given a response shaped
  any other way.
- A directory's `Dirent.Size`/`Stat.Size` comes back as `math.MaxUint64`
  (the protocol's own "invalid size" sentinel), not 0.
- A `log` response's `changed-path-entry` is a flat 4-element tuple —
  `( path:string mode:word ? ( copy-path:string copy-rev:number )
  ? ( node-kind:string text-mods:bool prop-mods:bool ) )` — with each
  optional group directly embedded, *not* nested an extra level the way
  `stat`'s entry above is. `path`/`copy-path` must be sent as
  `StringType`, not the `WordType` `Marshal` produces by default for a
  plain Go `string` — a bare word breaks on any path containing `/`.
- The `svn:entry:*` pseudo-properties a checkout sends
  (`committed-rev`/`committed-date`/`last-author`/`uuid`) are not optional
  polish: omitting `committed-rev` crashes a real `svn` client outright.
- A directory listing's self-entry must be identified structurally (the
  entry with the shortest `Path`), not by string-matching
  `"/" + the queried path`: the latter breaks for a session anchored below
  the repository root, since `Dirent.Path` is always repository-root-
  relative regardless of what was queried.
- `get-iprops` ("get inherited properties") must always be answered (an
  empty list is a fine answer, since neither `List` nor `GetFile` model
  directory properties at all) rather than gated behind a nil-able
  callback: a real client sends it for any checkout/update below the
  repository root and hangs forever without a reply.
- A real `svn switch` reparents its session several times before finally
  sending `switch` itself with an empty `target` and the destination as a
  full URL parameter — so `target` can't be trusted for the real
  destination path; compare the command's own `url`/`versusURL` argument
  against `Server.ReposInfo.URL` instead (`RepoRelativePath` in `url.go`).
- A `SwitchEdit` comparing two different repository paths must not treat
  a matching `CreatedRev` as proof a same-kind file is unchanged (unlike
  `UpdateEdit`, comparing the same path across revisions): two unrelated
  files can share a `CreatedRev` by pure coincidence.
- `"commit"`'s own deferred final response —
  `( new-rev:number [date] [author] [post-commit-err] )` — is sent
  **bare, with no `( success ( ... ) )` envelope**, unlike every other
  command's response in this codebase. A commit message (or any other
  revision-property value) sent alongside it must be built from raw
  `[]byte`, not a Go `string` field on a struct passed to `Marshal`, for
  the same word-vs-string reason noted above.

## The `fs.ErrNotExist` convention

`Client.Stat` reports a missing path as an error satisfying
`errors.Is(err, fs.ErrNotExist)`, because a real svnserve reports "stat" on
a nonexistent path as a **successful** response, not a protocol failure —
the outer tuple still has exactly one slot, holding an empty list rather
than the tuple itself having zero elements. `Server.Stat` callbacks are
expected to signal "not found" the same way; `Serve` then answers with
that same shape instead of a generic failure. Nothing else in this
codebase currently follows this convention — `List`/`GetFile` "not found"
still surface as ordinary protocol failures.

## Testing

Both directions of real-world interop are exercised, skipping cleanly when
the tools they need (`svnadmin`/`svn`/`svnserve`) aren't on `PATH`:

- **Our `Client` against a real server**: build a real, temporary
  repository with `svnadmin create` and a real `svn checkout`/`add`/
  `commit`, then exercise `Client`/`svnfs`/`go-svn`'s own subcommands
  against it through `Connect`'s `file://` handling (which execs a real
  `svnserve -t` — no daemon, no network).
- **Our `Server` against a real client**: run `Server.Serve` on a real
  `net.Listen("tcp", ...)` listener and drive a real `svn` client against
  it.

These tests found essentially every wire-shape quirk listed above, plus a
real `svn` client segfaulting on a malformed response — when an assumption
about the real protocol is in doubt, testing it this way beats reasoning
it out from the spec text or an in-memory fake alone. A wire capture only
shows real ordering between a write and the very next read that follows
it: batching several writes before reading anything back can make "sent
immediately" and "sent late" look identical.
