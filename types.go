package svn

// ReposInfo contains the general information in a repo.
// It is filled after the initial connection.
type ReposInfo struct {
	UUID         string
	URL          string
	Capabilities []string
}

// Stat is the response for a "stat" command
// (asking for the status of a path in a revision).
type Stat struct {
	Kind        string
	Size        uint64
	HasProps    bool
	CreatedRev  uint
	CreatedDate string
	LastAuthor  string
}

// Dirent is the response for the "list" command
// (asking for list of files)
type Dirent struct {
	Path        string
	Kind        string
	Size        uint64
	HasProps    bool
	CreatedRev  uint
	CreatedDate string
	LastAuthor  string
}

// PorpList is one of the responses for the "get-file" command
// (asking for the contents of a file)
type PropList struct {
	Name  string
	Value string
}
