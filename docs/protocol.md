# Protocol command reference

This is an exhaustive, command-by-command list of the
[ra_svn protocol](https://svn.apache.org/repos/asf/subversion/trunk/subversion/libsvn_ra_svn/protocol),
showing which ones `svn.Client` and `svn.Server` implement. For a
higher-level "which `svn` subcommands work" summary, see the
[Protocol coverage](../README.md#protocol-coverage) section of the README
instead — this file is the detailed reference underneath it. For *how*
the Report and Editor Command Set exchange is actually implemented on
each side, see [architecture.md](architecture.md) — this file sticks to
*what* is implemented, command by command.

Legend: ✅ implemented — ⚠️ partial (see notes) — ❌ not implemented.

## Main Command Set

These are the commands a client sends to ask the server to do something.

| Command | Client | Server | Notes |
| --- | --- | --- | --- |
| `reparent` | ❌ | ✅ | `Server.Reparent` callback, purely informational (like `set-path`): `Serve` itself has no notion of a session's own anchor, so this only matters to a `Server` implementation whose own callbacks resolve a path against a remembered one -- a real client commonly reparents an existing session, often more than once, while preparing a "switch" (see below), rather than opening a new connection. `Client` has no method to send this command |
| `get-latest-rev` | ✅ | ✅ | `Client.GetLatestRev`; `server.go`'s `"get-latest-rev"` case |
| `get-dated-rev` | ❌ | ❌ | |
| `change-rev-prop` | ❌ | ❌ | no revision-property support at all |
| `change-rev-prop2` | ❌ | ❌ | |
| `rev-proplist` | ❌ | ❌ | |
| `rev-prop` | ❌ | ❌ | |
| `commit` | ✅ | ✅ | `Client.Commit` sends this and drives the client's own end of the Editor Command Set; `server.go`'s `"commit"` case (`handleCommit`) does the reverse, via `Server.Commit`/`Server.FinishCommit` -- see [docs/architecture.md](docs/architecture.md) |
| `get-file` | ✅ | ✅ | `Client.GetFile`; `server.go`'s `"get-file"` case |
| `get-dir` | ❌ | ✅ | superseded by `list`, which `server.go`'s own directory-listing code uses internally too (this case is a thin wrapper around `Server.List`) -- a modern client generally sends `list` instead, but `svn diff` still falls back to `get-dir` to enumerate a deleted directory's former contents, despite `Server.Serve` always advertising the `list` capability |
| `check-path` | ❌ | ✅ | `Server.CheckPath` callback exists and is wired up, but `Client` has no method to send this command |
| `stat` | ✅ | ✅ | `Client.Stat`; `server.go`'s `"stat"` case. See the README's note on this command's real, twice-nested wire shape |
| `get-mergeinfo` | ❌ | ❌ | |
| `update` | ✅ for a plain checkout or a single-revision update | ✅ for a checkout or a single-revision update | The report/editor exchange that follows (`set-path`, ..., `finish-report`) drives a real "svn checkout" or "svn update", for a working copy that isn't "mixed revision" -- see [architecture.md](architecture.md) |
| `switch` | ❌ | ✅ for a single-revision switch | Same mechanism as `update`, diffing two different repository locations instead of two revisions of the same one -- see [architecture.md](architecture.md). `switch`'s own param order differs from `diff`'s (confirmed by capture): `( [rev] target recurse url ? depth send-copyfrom-args ignore-ancestry )`. `Client` has no method to send this command at all |
| `status` | ❌ | ❌ | note: this is the wire command a real client uses to compute local status, unrelated to this package's own `Server` type name |
| `diff` | ✅ for a single-revision comparison against this Client's own history | ✅ for a single-revision comparison | Reuses `update`'s own report/editor machinery entirely -- see [architecture.md](architecture.md). `versusURL`/`url` is `Client.address` client-side, matching a real client's own "same URL, two revisions" diff |
| `log` | ✅ | ✅ | `Client.Log`; `server.go`'s `"log"` case |
| `get-locations` | ❌ | ❌ | used by `svn blame`'s history-following across renames |
| `get-location-segments` | ❌ | ❌ | |
| `get-file-revs` | ❌ | ❌ | used by `svn blame`/`svn diff` |
| `lock` | ❌ | ❌ | |
| `lock-many` | ❌ | ❌ | |
| `unlock` | ❌ | ❌ | |
| `unlock-many` | ❌ | ❌ | |
| `get-lock` | ❌ | ❌ | |
| `get-locks` | ❌ | ❌ | |
| `replay` | ❌ | ❌ | |
| `replay-range` | ❌ | ❌ | |
| `get-deleted-rev` | ❌ | ❌ | |
| `get-iprops` | ❌ | ✅ | always reports no inherited properties -- neither `List` nor `GetFile` model a directory's own properties at all, so there's nothing to report. Unlike every other command, not gated behind a nil-able callback field: a real client sends this as part of *any* checkout/update below the repository root, and needs a real answer to complete it (confirmed: it otherwise fails outright with "Unknown command 'get-iprops'") |
| `list` | ✅ | ✅ | `Client.List`; `server.go`'s `"list"` case. See `Server.List`'s doc comment for this command's real wire shape (leading `/`, full repository-root-relative path, self-entry) |

## Report Command Set

Sent by a client to describe what it already has, driving an `update`,
`switch`, `status` or `diff`. `svn.Server` only goes as far as accepting
the ones needed to reach `finish-report`; `svn.Client.Checkout`/`Update`/
`Diff` send `set-path`/`finish-report` too, but only ever one of two fixed
shapes: `Checkout` always reports "I have nothing" (start-empty), and
`Update`/`Diff` (structurally identical reports, driven by different Main
Command Set commands) always report an existing directory as being
entirely at one uniform revision -- none of the three has any way to
describe a real, per-subtree-mixed-revision working copy.

| Command | Client | Server | Notes |
| --- | --- | --- | --- |
| `set-path` | ✅ for `Checkout`'s/`Update`'s/`Diff`'s own fixed report shapes | ⚠️ | `Serve` accumulates every `set-path` call into a `[]ReportedPath`, passed to `FinishReport` once the report ends; `Server.SetPath` itself is optional and purely informational (e.g. logging) -- it does not need to be set for the accumulation to happen. `Client.Checkout` always sends a single, start-empty, root `set-path`; `Client.Update`/`Client.Diff` send the same but with `start-empty` false and the caller's own `fromRev` |
| `delete-path` | ❌ | ❌ | no case in `server.go`'s switch: replies "Unknown command"; not yet folded into `ReportedPath` accumulation |
| `link-path` | ❌ | ❌ | same |
| `finish-report` | ✅ | ✅ for a checkout, or a single-revision update/diff/switch | `Server.FinishReport` builds the resulting `[]Item` via `CheckoutEdit`/`UpdateEdit`/`SwitchEdit` (see [architecture.md](architecture.md)); a mixed-revision report still needs a caller-supplied `EditorWriter` sequence. `Client.Checkout`/`Update`/`Diff` send this and then read the resulting Editor Command Set themselves |
| `abort-report` | ❌ | ❌ | no case in `server.go`'s switch |

## Editor Command Set

Describes a tree of changes, one command per node touched. Used in two
directions: server → client while driving an `update`/`switch` (after
`finish-report`), and client → server while performing a `commit`.
`EditorWriter` (`editor.go`) builds a sequence to *send*, used
server-side by `CheckoutEdit`/`UpdateEdit`/`SwitchEdit` and client-side by
`Client.Commit`; `Editor` + `driveEditor` (`client_editor.go`) *parse* an
incoming sequence, used by `Client.Checkout`/`Update`/`Diff` — there is no
`Server`-side equivalent yet, since nothing in this package can *receive*
a commit. See [architecture.md](architecture.md) for how these
pieces fit together and the wire-level gotchas found building them (in
particular: a `path` argument is always the full path from the edit's
root, tokens are arbitrary, and `"commit"`'s own deferred final response
is sent unwrapped, unlike every other command's).

The table below covers the server → client direction (`Client`'s own
parsing side, `Server`'s own writing side via `EditorWriter`);
`Client.Commit`'s reverse use of `EditorWriter` to *write* this same
command set isn't a separate column, since it's the exact same builder,
just fed to `Commit` instead of returned from `FinishReport`.

| Command | Client | Server |
| --- | --- | --- |
| `target-rev` | ✅ | ⚠️ `EditorWriter.TargetRev` builds the `Item`; nothing sends it automatically |
| `open-root` | ✅ calls `Editor.OpenRoot` (a no-op if left nil) | ⚠️ `EditorWriter.OpenRoot` |
| `delete-entry` | ✅ calls `Editor.DeleteEntry` | ⚠️ `EditorWriter.DeleteEntry` |
| `add-dir` | ✅ calls `Editor.AddDir` | ⚠️ `EditorWriter.AddDir` |
| `open-dir` | ✅ calls `Editor.OpenDir` | ⚠️ `EditorWriter.OpenDir` |
| `change-dir-prop` | ✅ calls `Editor.ChangeDirProp` | ⚠️ `EditorWriter.ChangeDirProp` |
| `close-dir` | ✅ calls `Editor.CloseDir` (needs no open-node stack to pop -- see above) | ⚠️ `EditorWriter.CloseDir` |
| `absent-dir` | ✅ calls `Editor.AbsentDir` | ⚠️ `EditorWriter.AbsentDir` |
| `add-file` | ✅ calls `Editor.AddFile` (a brand new file: no base content for a later `apply-textdelta`) | ⚠️ `EditorWriter.AddFile` |
| `open-file` | ✅ calls `Editor.OpenFile`, whose own return value becomes `decodeSvndiff`'s source | ⚠️ `EditorWriter.OpenFile` |
| `apply-textdelta` | ✅ calls `Editor.ApplyTextdelta`; verifies the optional base checksum against `Editor.OpenFile`'s own returned content first | ⚠️ `EditorWriter.ApplyTextdelta` (always a single, sourceless `EncodeSvndiff` window -- no incremental delta against a real base yet) |
| `textdelta-chunk` | ✅ (no matching `Editor` field: accumulated internally, decoded and handed to `Editor.CloseFile` as `close-file`'s own `content`) | ⚠️ emitted by `EditorWriter.ApplyTextdelta`, not its own method |
| `textdelta-end` | ⚠️ parsed but a no-op (decoding happens at `close-file` instead) | ⚠️ same |
| `change-file-prop` | ✅ calls `Editor.ChangeFileProp` | ⚠️ `EditorWriter.ChangeFileProp` |
| `close-file` | ✅ calls `Editor.CloseFile` with the fully decoded content; verifies the optional checksum against it first | ⚠️ `EditorWriter.CloseFile` |
| `absent-file` | ✅ calls `Editor.AbsentFile` | ⚠️ `EditorWriter.AbsentFile` |
| `close-edit` | ✅ acks it and reads `finish-report`'s own final response, exactly like a real client | ⚠️ always sent automatically at the end of a successful `finish-report`, and its client ack read, before `Serve` answers `finish-report` itself; the command itself is never parsed |
| `abort-edit` | ✅ acked the same way as `close-edit`, reporting the checkout as aborted | ⚠️ same, if a `finish-report` callback errors instead: sent automatically, its client ack read (a real client sends one either way -- confirmed the hard way), before answering `finish-report`; never parsed |
| `finish-replay` | ❌ | ❌ |

## Auth mechanisms

| Mechanism | Client | Server |
| --- | --- | --- |
| `ANONYMOUS` | ✅ | ✅ (offered; accepts any response without checking it) |
| `EXTERNAL` | ✅ (preferred) | ✅ (offered; accepts any response without checking it) |
| `CRAM-MD5` / password auth | ❌ | ❌ |

`Server` never actually validates credentials for any mechanism it offers
— see `server.go`'s handshake code — so it only makes sense to use it
where the transport itself already establishes trust (e.g. `svn+ssh://`'s
SSH layer), or where anonymous access is genuinely fine.
