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
| `reparent` | ❌ | ❌ | |
| `get-latest-rev` | ✅ | ✅ | `Client.GetLatestRev`; `server.go`'s `"get-latest-rev"` case |
| `get-dated-rev` | ❌ | ❌ | |
| `change-rev-prop` | ❌ | ❌ | no revision-property support at all |
| `change-rev-prop2` | ❌ | ❌ | |
| `rev-proplist` | ❌ | ❌ | |
| `rev-prop` | ❌ | ❌ | |
| `commit` | ❌ | ❌ | no write support anywhere in this package |
| `get-file` | ✅ | ✅ | `Client.GetFile`; `server.go`'s `"get-file"` case |
| `get-dir` | ❌ | ❌ | superseded by `list` (below), which both sides use instead; `Server.Serve` always advertises the `list` capability, so a modern client won't send `get-dir` anyway |
| `check-path` | ❌ | ✅ | `Server.CheckPath` callback exists and is wired up, but `Client` has no method to send this command |
| `stat` | ✅ | ✅ | `Client.Stat`; `server.go`'s `"stat"` case. See the README's note on this command's real, twice-nested wire shape |
| `get-mergeinfo` | ❌ | ❌ | |
| `update` | ❌ | ⚠️ | `Server.Update` callback is invoked with the parsed arguments, but it has no return value and the report/editor exchange that would drive an actual update isn't implemented, so nothing meaningful can happen as a result; `Client` has no method to send this at all |
| `switch` | ❌ | ❌ | needs the same report/editor exchange as `update` |
| `status` | ❌ | ❌ | note: this is the wire command a real client uses to compute local status, unrelated to this package's own `Server` type name |
| `diff` | ❌ | ❌ | |
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
| `get-iprops` | ❌ | ❌ | inherited properties |
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
| `finish-report` | ❌ | ⚠️ | `Server.FinishReport` callback receives the accumulated `[]ReportedPath` and its returned `[]Item` is written to the wire followed by `close-edit` (or `abort-edit` on error) — but constructing a correct Editor Command Set sequence by hand, as raw `Item`s, is still the caller's job entirely; nothing in this package helps build one |
| `abort-report` | ❌ | ❌ | no case in `server.go`'s switch |

## Editor Command Set

Describes a tree of changes, one command per node touched. Used in two
directions: server → client while driving an `update`/`switch` (after
`finish-report`), and client → server while performing a `commit`. Neither
direction PARSES any of these (nothing in this package reads an Editor
Command Set sequence sent to it); generating the server → client
direction is what `EditorWriter` (`editor.go`) is for -- one typed method
per command below, building up the `[]Item` a `Server.FinishReport`
implementation can return -- but it isn't wired into `Serve` yet: nothing
calls it automatically, and there is still no way to drive an actual
`update`/`switch`/`checkout` end to end. `close-edit`/`abort-edit`
specifically are always sent automatically by `server.go` itself (not via
`EditorWriter`) to end the exchange once `FinishReport` returns.

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
| `close-edit` | ❌ | ⚠️ always sent automatically at the end of a successful `finish-report`; never parsed |
| `abort-edit` | ❌ | ⚠️ always sent automatically if a `finish-report` callback errors; never parsed |
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
