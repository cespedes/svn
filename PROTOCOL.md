# Protocol command reference

This is an exhaustive, command-by-command list of the
[ra_svn protocol](https://svn.apache.org/repos/asf/subversion/trunk/subversion/libsvn_ra_svn/protocol),
showing which ones `svn.Client` and `svn.Server` implement. For a
higher-level "which `svn` subcommands work" summary, see the
[Protocol coverage](README.md#protocol-coverage) section of the README
instead — this file is the detailed reference underneath it.

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
| `commit` | ❌ | ❌ | no write support anywhere in this package |
| `get-file` | ✅ | ✅ | `Client.GetFile`; `server.go`'s `"get-file"` case |
| `get-dir` | ❌ | ✅ | superseded by `list`, which `server.go`'s own directory-listing code uses internally too (this case is a thin wrapper around `Server.List`) -- a modern client generally sends `list` instead, but `svn diff` still falls back to `get-dir` to enumerate a deleted directory's former contents, despite `Server.Serve` always advertising the `list` capability |
| `check-path` | ❌ | ✅ | `Server.CheckPath` callback exists and is wired up, but `Client` has no method to send this command |
| `stat` | ✅ | ✅ | `Client.Stat`; `server.go`'s `"stat"` case. See the README's note on this command's real, twice-nested wire shape |
| `get-mergeinfo` | ❌ | ❌ | |
| `update` | ❌ | ✅ for a checkout or a single-revision update | `Server.Update` callback is invoked with the parsed arguments and has no return value, but the report/editor exchange that follows (`set-path`, ..., `finish-report`) now drives a real, working "svn checkout" or "svn update" (for a working copy that isn't "mixed revision") -- see the Report/Editor Command Set sections below. `Client` has no method to send this command at all |
| `switch` | ❌ | ✅ for a single-revision switch | `Server.Switch` callback is invoked with the parsed arguments and has no return value; the report/editor exchange that follows drives the switch via `Server.SwitchEdit`, the same report/editor mechanism `update` uses but diffing two different repository locations (by name, not shared history) instead of two revisions of the same one -- see the Editor Command Set section below. Confirmed by raw wire capture that `switch`'s own param order differs from `diff`'s: `( [rev] target recurse url ? depth send-copyfrom-args ignore-ancestry )`, with `depth`/`send-copyfrom-args`/`ignore-ancestry` all coming after `url`, not interleaved with `recurse`/`ignore-ancestry` the way `diff`'s params are. `Client` has no method to send this command at all |
| `status` | ❌ | ❌ | note: this is the wire command a real client uses to compute local status, unrelated to this package's own `Server` type name |
| `diff` | ❌ | ✅ for a single-revision comparison | `Server.Diff` callback is invoked with the parsed arguments and has no return value; the report/editor exchange that follows drives the diff, via the exact same `IsSingleRevisionUpdate`/`UpdateEdit` machinery "update" uses (the accumulated report looks identical either way -- see the Report/Editor Command Set sections below). `Client` has no method to send this command at all |
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
`switch`, `status` or `diff`. `svn.Client` never sends any of these (it
never initiates the exchange in the first place, via `update`/`switch`
above); `svn.Server` only goes as far as accepting the ones needed to
reach `finish-report`.

| Command | Client | Server | Notes |
| --- | --- | --- | --- |
| `set-path` | ❌ | ⚠️ | `Serve` accumulates every `set-path` call into a `[]ReportedPath`, passed to `FinishReport` once the report ends; `Server.SetPath` itself is optional and purely informational (e.g. logging) -- it does not need to be set for the accumulation to happen |
| `delete-path` | ❌ | ❌ | no case in `server.go`'s switch: replies "Unknown command"; not yet folded into `ReportedPath` accumulation |
| `link-path` | ❌ | ❌ | same |
| `finish-report` | ❌ | ✅ for a checkout, or a single-revision update/diff/switch | `Server.FinishReport` callback receives the accumulated `[]ReportedPath`; for the shape `IsPlainCheckout` recognizes, `Server.CheckoutEdit` builds the resulting `[]Item` automatically, and for the shape `IsSingleRevisionUpdate` recognizes (a working copy that isn't "mixed revision"), `Server.UpdateEdit`/`Server.SwitchEdit` do, by diffing the client's revision against the target one, or against a different repository location, respectively (see the Editor Command Set section below) -- this same shape, and so the same `IsSingleRevisionUpdate` call, is what both a "diff" and a "switch" command's report reduce to as well. A mixed-revision report still needs a caller-supplied `EditorWriter` sequence |
| `abort-report` | ❌ | ❌ | no case in `server.go`'s switch |

## Editor Command Set

Describes a tree of changes, one command per node touched. Used in two
directions: server → client while driving an `update`/`switch` (after
`finish-report`), and client → server while performing a `commit`. Neither
direction PARSES any of these (nothing in this package reads an Editor
Command Set sequence sent to it); generating the server → client
direction is what `EditorWriter` (`editor.go`) is for -- one typed method
per command below, building up the `[]Item` a `Server.FinishReport`
implementation can return. For the specific case a checkout's report
always reduces to, `Server.CheckoutEdit` (`checkout.go`) drives
`EditorWriter` automatically, walking the target revision's tree via
`List`/`GetFile` and describing every node as newly added. For a real
"update" of a single-revision (not mixed-revision) working copy,
`Server.UpdateEdit` (`update.go`) does the same but diffs both revisions
of the tree, only describing what changed: `AddDir`/`AddFile` for a new
node, `DeleteEntry` for a removed one, `OpenDir`/`OpenFile` (not the
`Add*` variant) plus a fresh `ApplyTextdelta` for a modified one, and
nothing at all for an unmodified file (confirmed against a real svnserve:
it's skipped entirely, never even opened). `UpdateEdit`'s `target`
parameter additionally handles a client naming one nested file or
subdirectory instead of its whole working copy (`svn update path/to/file`,
`svn diff path/to/file`): every path segment strictly between the
report's own root and the target is walked (via `List`, to find the
target and know whether it changed) but never itself described in the
editor sequence -- no `open-dir`/`add-dir`, no entry-props -- since a real
client computes the target's own local path by joining every directory
name it receives and expects the target's parent to coincide with the
edit's root regardless of how many real path segments separate them;
describing an intermediate directory instead produces a doubled, bogus
local path that a real client rejects outright once it tries to apply the
edit. `target` itself differs in shape between the two commands that set
it: "update" always anchors a fresh session exactly at the target's own
parent directory, so its `target` argument reliably names only a single
path segment, but "diff" often reuses an existing, possibly
higher-anchored session while still reporting `target` as that same bare
child name -- so a `Server.Diff` callback needs to recover the target's
real, possibly multi-segment path itself, typically via `RepoRelativePath`
(comparing the command's own `versusURL` argument against
`Server.ReposInfo.URL`). Both are confirmed end to end against a real
`svn checkout`/`svn update`/`svn diff`, including of a single nested file
(`TestServerAgainstRealSVNClient` in `server_integration_test.go`).
`Server.SwitchEdit` (`update.go`, alongside `UpdateEdit` -- both share
most of their implementation, factored into an unexported `diffEdit`)
does the same as `UpdateEdit`, except that its "from" and "to" sides can
be two entirely different repository locations (e.g. "trunk" and
"branches/foo") rather than the same path at two revisions: a node
present under both is still compared by name and diffed similarly to how
`UpdateEdit` does (delete-then-add if its kind doesn't match), with no
attempt to detect a rename or otherwise use copy ancestry, since the two
sides are walked purely structurally. It does *not*, however, treat a
matching `CreatedRev` as proof that a same-kind file is unchanged the way
`UpdateEdit` does: that's only sound when it's genuinely the same path
across two revisions, but two files at different paths can share a
`CreatedRev` by pure coincidence (e.g. both added in the same commit)
despite having unrelated content -- an earlier version of this code
reused `UpdateEdit`'s own rule regardless, which could send a client
switching to such a file its old, `fromPath` content instead of the new
one (see `TestSwitchEditDoesNotSkipSameCreatedRev` in `switch_test.go`).
So `SwitchEdit` always resends a same-kind file's full content instead,
at the cost of occasionally resending one that's genuinely identical on
both sides. Its own
`target` parameter works the same way `UpdateEdit`'s does, navigating
down from both sides in parallel by the same segment names. Confirmed
end to end against a real `svn switch`, including that a real client
generally reparents an existing session -- see `reparent` above -- back
to align with the *source* side right before actually sending `switch`,
so a `Server.Switch` callback needs to recover the real destination path
from the command's own `url` argument, the same way a `Server.Diff`
callback already needs to for `versusURL` (see `RepoRelativePath`), not
from `target`. A mixed-revision `update` still needs a caller to drive
`EditorWriter` itself, since it isn't implemented. `close-edit`/
`abort-edit` specifically are always sent automatically by `server.go`
itself (not via `EditorWriter`) to end the exchange once `FinishReport`
returns.

| Command | Client | Server |
| --- | --- | --- |
| `target-rev` | ❌ | ⚠️ `EditorWriter.TargetRev` builds the `Item`; nothing sends it automatically |
| `open-root` | ❌ | ⚠️ `EditorWriter.OpenRoot` |
| `delete-entry` | ❌ | ⚠️ `EditorWriter.DeleteEntry` |
| `add-dir` | ❌ | ⚠️ `EditorWriter.AddDir` |
| `open-dir` | ❌ | ⚠️ `EditorWriter.OpenDir` |
| `change-dir-prop` | ❌ | ⚠️ `EditorWriter.ChangeDirProp` |
| `close-dir` | ❌ | ⚠️ `EditorWriter.CloseDir` |
| `absent-dir` | ❌ | ⚠️ `EditorWriter.AbsentDir` |
| `add-file` | ❌ | ⚠️ `EditorWriter.AddFile` |
| `open-file` | ❌ | ⚠️ `EditorWriter.OpenFile` |
| `apply-textdelta` | ❌ | ⚠️ `EditorWriter.ApplyTextdelta` (always a single, sourceless `EncodeSvndiff` window -- no incremental delta against a real base yet) |
| `textdelta-chunk` | ❌ | ⚠️ emitted by `EditorWriter.ApplyTextdelta`, not its own method |
| `textdelta-end` | ❌ | ⚠️ same |
| `change-file-prop` | ❌ | ⚠️ `EditorWriter.ChangeFileProp` |
| `close-file` | ❌ | ⚠️ `EditorWriter.CloseFile` |
| `absent-file` | ❌ | ⚠️ `EditorWriter.AbsentFile` |
| `close-edit` | ❌ | ⚠️ always sent automatically at the end of a successful `finish-report`, and its client ack read, before `Serve` answers `finish-report` itself; the command itself is never parsed |
| `abort-edit` | ❌ | ⚠️ same, if a `finish-report` callback errors instead: sent automatically, its client ack read (a real client sends one either way -- confirmed the hard way), before answering `finish-report`; never parsed |
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
