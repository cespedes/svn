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
| `update` | ✅ for a plain checkout or a single-revision update | ✅ for a checkout or a single-revision update | `Server.Update` callback is invoked with the parsed arguments and has no return value, but the report/editor exchange that follows (`set-path`, ..., `finish-report`) now drives a real, working "svn checkout" or "svn update" (for a working copy that isn't "mixed revision") -- see the Report/Editor Command Set sections below. `Client.Checkout`/`Update` send this too: `Checkout` reports "I have nothing" (the same shape `IsPlainCheckout` recognizes), `Update` reports an existing directory as being entirely at one revision (the same shape `IsSingleRevisionUpdate` recognizes); both then apply the resulting Editor Command Set sequence the same way -- see the Editor Command Set section below for how that parsing side works |
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
`switch`, `status` or `diff`. `svn.Server` only goes as far as accepting
the ones needed to reach `finish-report`; `svn.Client.Checkout`/`Update`
send `set-path`/`finish-report` too, but only ever one of two fixed
shapes: `Checkout` always reports "I have nothing" (start-empty), and
`Update` always reports an existing directory as being entirely at one
uniform revision -- neither has any way to describe a real,
per-subtree-mixed-revision working copy, or a `switch`/`diff`'s own
report (structurally identical to `update`'s, but driven by a different
Main Command Set command).

| Command | Client | Server | Notes |
| --- | --- | --- | --- |
| `set-path` | ✅ for `Checkout`'s/`Update`'s own fixed report shapes | ⚠️ | `Serve` accumulates every `set-path` call into a `[]ReportedPath`, passed to `FinishReport` once the report ends; `Server.SetPath` itself is optional and purely informational (e.g. logging) -- it does not need to be set for the accumulation to happen. `Client.Checkout` always sends a single, start-empty, root `set-path`; `Client.Update` sends the same but with `start-empty` false and the caller's own `fromRev` |
| `delete-path` | ❌ | ❌ | no case in `server.go`'s switch: replies "Unknown command"; not yet folded into `ReportedPath` accumulation |
| `link-path` | ❌ | ❌ | same |
| `finish-report` | ✅ | ✅ for a checkout, or a single-revision update/diff/switch | `Server.FinishReport` callback receives the accumulated `[]ReportedPath`; for the shape `IsPlainCheckout` recognizes, `Server.CheckoutEdit` builds the resulting `[]Item` automatically, and for the shape `IsSingleRevisionUpdate` recognizes (a working copy that isn't "mixed revision"), `Server.UpdateEdit`/`Server.SwitchEdit` do, by diffing the client's revision against the target one, or against a different repository location, respectively (see the Editor Command Set section below) -- this same shape, and so the same `IsSingleRevisionUpdate` call, is what both a "diff" and a "switch" command's report reduce to as well. A mixed-revision report still needs a caller-supplied `EditorWriter` sequence. `Client.Checkout`/`Update` send `finish-report` and then read the resulting Editor Command Set sequence themselves (see below, `driveEditor`), acking `close-edit`/`abort-edit` and reading the response to `finish-report` itself exactly the way a real client does |
| `abort-report` | ❌ | ❌ | no case in `server.go`'s switch |

## Editor Command Set

Describes a tree of changes, one command per node touched. Used in two
directions: server → client while driving an `update`/`switch` (after
`finish-report`), and client → server while performing a `commit`. Only
the client → server, `commit` direction is entirely unparsed and
unproduced by either side (no write support anywhere in this package).
Generating the server → client direction is what `EditorWriter`
(`editor.go`) is for -- one typed method per command below, building up
the `[]Item` a `Server.FinishReport` implementation can return -- while
*parsing* that same direction is what `driveEditor` (`client_editor.go`)
does, shared by `Client.Checkout` (`client_checkout.go`) and
`Client.Update` (`client_update.go`): it reads the sequence a real
svnserve's own `finish-report` streams back and calls one field of a
caller-supplied `Editor` (also `client_editor.go`) per command -- the
first thing in this package that reads an Editor Command Set at all
rather than only ever writing one. `Editor` is a struct of callback
fields, one per command below, mirroring `Server`'s own style exactly:
`driveEditor` itself never touches a filesystem, a database, or anything
else -- see `Editor`'s own doc comment, and cmd/go-svn's own
`checkout`/`update` subcommands for a filesystem-backed implementation
built purely on this API, which is deliberately the *only* place in this
codebase that decides what "checking out" or "updating" concretely
means. `driveEditor` keeps no token→node mapping for directories at all:
every `add-dir`/`open-dir`/`add-file`/`open-file`'s own `path` is already
the full path from the edit's root (see below), so it's passed straight
through to `Editor`'s own callbacks; `close-dir` needs no bookkeeping to
match either. A file's own wire token *is* tracked internally, purely to
correlate `apply-textdelta`/`textdelta-chunk`/`textdelta-end`/
`close-file` back to the same path (since those commands only ever carry
a token, not a path) -- `Editor`'s own callbacks never see a token at
all, only the path it already resolves to. Properties
(`change-dir-prop`/`change-file-prop`) and `absent-dir`/`absent-file` are
parsed and handed to `Editor`'s own matching fields (a nil field simply
skips the call). `apply-textdelta`'s content is decoded via
`decodeSvndiff` (`svndiff.go` -- promoted out of test-only status for
this): `Editor.OpenFile` (called for an already-existing file, only ever
sent to `Update`, never `Checkout`) must return that file's own current
content itself, since a real svnserve updating an existing file may
describe the new content as a genuine incremental delta against it
rather than a full replacement -- confirmed the hard way (a first
version always decoded against `nil`, as `add-file`'s own brand-new-file
case correctly does, which made `TestClientUpdate` fail decoding a real
svnserve's own delta for a modified file with "source view out of
range"). `apply-textdelta`'s own optional base checksum, if present, is
verified automatically against whatever `Editor.OpenFile` returned;
`close-file`'s optional checksum, if present, is verified automatically
against the newly decoded content, before `Editor.CloseFile` is ever
called with it. For the specific case a checkout's report
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
