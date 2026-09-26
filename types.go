package svn

// ReposInfo contains the general information in a repo.
// It is filled after the initial connection.
type ReposInfo struct {
	// UUID uniquely identifies the repository, regardless of the URL used
	// to reach it.
	UUID string
	// URL is the repository's root URL, which may be a parent of the URL
	// used to connect (if that URL pointed to a subdirectory).
	URL string
	// Capabilities lists the protocol capability words the server
	// advertised (e.g. "depth", "mergeinfo", "log-revprops").
	Capabilities []string
}

// Stat is the response for a "stat" command
// (asking for the status of a path in a revision).
type Stat struct {
	// Kind is the node kind: "file", "dir", or "none" if the path does not
	// exist at the requested revision.
	Kind string
	// Size is the file's size in bytes. It is meaningless for directories.
	Size uint64
	// HasProps reports whether the node has any versioned properties.
	HasProps bool
	// CreatedRev is the revision in which the node was last changed.
	CreatedRev uint
	// CreatedDate is the commit date of CreatedRev, as an ISO 8601
	// timestamp (e.g. "2024-04-02T13:37:34.350221Z").
	CreatedDate string
	// LastAuthor is the value of the svn:author revision property for
	// CreatedRev.
	LastAuthor string
}

// Dirent is the response for the "list" command
// (asking for list of files).
type Dirent struct {
	// Path is the entry's path, relative to the directory that was listed.
	Path string
	// Kind is the node kind: "file" or "dir".
	Kind string
	// Size is the file's size in bytes. It is meaningless for directories.
	Size uint64
	// HasProps reports whether the node has any versioned properties.
	HasProps bool
	// CreatedRev is the revision in which the node was last changed.
	CreatedRev uint
	// CreatedDate is the commit date of CreatedRev, as an ISO 8601
	// timestamp (e.g. "2024-04-02T13:37:34.350221Z").
	CreatedDate string
	// LastAuthor is the value of the svn:author revision property for
	// CreatedRev.
	LastAuthor string
}

// PropList is one of the responses for the "get-file" command
// (asking for the contents of a file).
type PropList struct {
	// Name is the property name, e.g. "svn:mime-type".
	Name string
	// Value is the property's value.
	Value string
}

// ChangedPath describes one path affected by a revision, as returned in a
// LogEntry's Changed field. On the wire, this is a fixed 4-element tuple
// -- path, mode, an optional copy-from group, an optional node-info group
// -- confirmed against a real svnserve (a Word here, instead of the
// length-prefixed String a real server always sends, breaks on any path
// containing a character a bare word can't hold, e.g. '/').
type ChangedPath struct {
	// Path is the affected path, relative to the repository root.
	Path string
	// Mode is a single-letter change type, as used by "svn log -v" (e.g.
	// "A" added, "D" deleted, "M" modified, "R" replaced).
	Mode string
	// Copy holds the path and revision this entry was copied from
	// (typically alongside Mode "A" or "R"), or nil if it wasn't a copy.
	Copy *ChangedPathCopy
	// Info holds the node kind and modification flags, if the server
	// included them (a real svnserve always does).
	Info *ChangedPathInfo
}

// ChangedPathCopy is a ChangedPath's copy-from source, when it has one.
type ChangedPathCopy struct {
	// Path is the source path this entry was copied from.
	Path string
	// Rev is the revision it was copied from.
	Rev uint
}

// ChangedPathInfo is a ChangedPath's node kind and modification flags,
// when the server included them.
type ChangedPathInfo struct {
	// NodeKind is "file" or "dir".
	NodeKind string
	// TextMods reports whether the node's content changed in this
	// revision.
	TextMods bool
	// PropMods reports whether the node's properties changed in this
	// revision.
	PropMods bool
}

// LogEntry is every one of the responses for the "log" command.
type LogEntry struct {
	// Changed lists the paths that were added, modified, deleted or
	// replaced in this revision. It is only populated when the "log"
	// command was called with changedPaths set to true.
	Changed []ChangedPath
	// Rev is the revision number.
	Rev uint
	// Author is the value of the svn:author revision property.
	Author string
	// Date is the value of the svn:date revision property, as an ISO 8601
	// timestamp.
	Date string
	// Message is the value of the svn:log revision property (the commit
	// message).
	Message string
}
