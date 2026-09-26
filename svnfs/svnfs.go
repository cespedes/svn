// Package svnfs adapts a [svn.Client] into a read-only [io/fs.FS], so
// standard-library tools (http.FileServerFS, fs.WalkDir, fs.Glob, ...) can
// browse and read files straight out of an SVN repository.
package svnfs

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cespedes/svn"
)

// FS adapts a [svn.Client] into a read-only [io/fs.FS], rooted at whatever
// directory the client is connected to, at a fixed revision.
//
// FS is safe for concurrent use by multiple goroutines, to the same extent
// its underlying Client is: a [svn.Client] locks around each command, so
// concurrent Open/Stat/ReadDir/ReadFile calls sharing one Client won't
// corrupt the connection, but they still only run one at a time (the
// protocol has no way to pipeline or multiplex commands over a single
// connection). For real concurrency rather than just safety, use a pool of
// FS/Client pairs instead of sharing one.
type FS struct {
	Client *svn.Client
	// Rev pins every operation to a specific revision. A nil Rev means
	// "the latest revision", evaluated independently by each call, so two
	// calls made moments apart could observe different content.
	Rev *int
}

// New returns an FS backed by c, at rev (or the latest revision, if rev is
// nil).
func New(c *svn.Client, rev *int) *FS {
	return &FS{Client: c, Rev: rev}
}

var (
	_ fs.FS         = (*FS)(nil)
	_ fs.StatFS     = (*FS)(nil)
	_ fs.ReadDirFS  = (*FS)(nil)
	_ fs.ReadFileFS = (*FS)(nil)
)

// toSVNPath converts a valid io/fs path (as constrained by [fs.ValidPath])
// into the path svn.Client expects: the root "." becomes "", and every
// other path is already in svn's own slash-separated, non-rooted form.
func toSVNPath(name string) string {
	if name == "." {
		return ""
	}
	return name
}

// Open implements [io/fs.FS]. Opening a directory returns a file whose
// ReadDir method serves its entries; opening a regular file fetches its
// entire content upfront, since the SVN protocol has no way to fetch part
// of a file, and returns a seekable [io/fs.File] over that buffered
// content (so http.ServeContent-style range requests work).
func (f *FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	info, err := f.stat(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if info.IsDir() {
		entries, err := f.readDir(name)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &openDir{info: info, entries: entries}, nil
	}
	_, content, err := f.Client.GetFile(toSVNPath(name), f.Rev, false, true)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &openFile{info: info, r: bytes.NewReader(content)}, nil
}

// Stat implements [io/fs.StatFS].
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	info, err := f.stat(name)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	return info, nil
}

// ReadFile implements [io/fs.ReadFileFS].
func (f *FS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	info, err := f.stat(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if info.IsDir() {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("is a directory")}
	}
	_, content, err := f.Client.GetFile(toSVNPath(name), f.Rev, false, true)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return content, nil
}

// ReadDir implements [io/fs.ReadDirFS].
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	info, err := f.stat(name)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errors.New("not a directory")}
	}
	entries, err := f.readDir(name)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	return entries, nil
}

// stat calls a Stat first, both to check existence/kind and to translate a
// missing path into fs.ErrNotExist -- svn.Client.Stat already does this,
// via errors.Is, so this is a thin, name-aware wrapper around it.
func (f *FS) stat(name string) (*fileInfo, error) {
	s, err := f.Client.Stat(toSVNPath(name), f.Rev)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	if s.Kind == "none" {
		// Defensive: this client's Stat currently reports an absent path
		// as an error rather than a successful "none" kind, but nothing
		// stops another server from doing the latter.
		return nil, fs.ErrNotExist
	}
	return newFileInfo(path.Base(name), s.Kind, s.Size, s.CreatedRev, s.CreatedDate), nil
}

// readDir lists the directory at name and returns its direct children as
// DirEntry values, with bare (non-prefixed) names.
func (f *FS) readDir(name string) ([]fs.DirEntry, error) {
	svnPath := toSVNPath(name)
	dirents, err := f.Client.List(svnPath, f.Rev, "immediates",
		[]string{"kind", "size", "created-rev", "time", "last-author"})
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, 0, len(dirents))
	for _, d := range dirents {
		// Some servers return a Dirent.Path already relative to svnPath
		// (a bare child name); at least one real svnserve has been seen
		// to return it prefixed with the full path we asked for instead
		// (the same discrepancy cmd/go-svn's own "ls" subcommand works
		// around). Strip the prefix if present, so either way we end up
		// with a bare child name.
		childName := d.Path
		if svnPath != "" {
			childName = strings.TrimPrefix(childName, svnPath+"/")
		}
		childName = strings.TrimPrefix(childName, "/")
		if childName == "" {
			// The queried directory listing itself, if the server
			// includes it: not one of its own children.
			continue
		}
		info := newFileInfo(childName, d.Kind, d.Size, d.CreatedRev, d.CreatedDate)
		entries = append(entries, fs.FileInfoToDirEntry(info))
	}
	// io/fs requires ReadDir results sorted by filename, as os.ReadDir
	// does; the wire order otherwise depends on the server (and, for the
	// in-memory test server in this package, on Go's random map order).
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// parseSVNDate parses one of svn's ISO-8601 revision dates. An empty or
// unparsable date (e.g. because the field wasn't requested) yields the
// zero Time rather than an error, since it is only ever used for
// FileInfo.ModTime, not for anything that must be trusted.
func parseSVNDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// fileInfo implements [io/fs.FileInfo] over the fields common to
// [svn.Stat] and [svn.Dirent].
type fileInfo struct {
	name       string
	kind       string
	size       uint64
	createdRev uint
	modTime    time.Time
}

func newFileInfo(name, kind string, size uint64, createdRev uint, createdDate string) *fileInfo {
	return &fileInfo{
		name:       name,
		kind:       kind,
		size:       size,
		createdRev: createdRev,
		modTime:    parseSVNDate(createdDate),
	}
}

func (fi *fileInfo) Name() string       { return fi.name }
func (fi *fileInfo) Size() int64        { return int64(fi.size) }
func (fi *fileInfo) IsDir() bool        { return fi.kind == "dir" }
func (fi *fileInfo) ModTime() time.Time { return fi.modTime }
func (fi *fileInfo) Sys() any           { return nil }

func (fi *fileInfo) Mode() fs.FileMode {
	if fi.IsDir() {
		return fs.ModeDir | 0555
	}
	return 0444
}

var _ fs.FileInfo = (*fileInfo)(nil)

// openFile is the [io/fs.File] returned by Open for a regular file: its
// entire content, fetched upfront, buffered and made seekable.
type openFile struct {
	info *fileInfo
	r    *bytes.Reader
}

func (of *openFile) Stat() (fs.FileInfo, error) { return of.info, nil }
func (of *openFile) Read(p []byte) (int, error) { return of.r.Read(p) }
func (of *openFile) Close() error               { return nil }

func (of *openFile) Seek(offset int64, whence int) (int64, error) {
	return of.r.Seek(offset, whence)
}

var (
	_ fs.File   = (*openFile)(nil)
	_ io.Seeker = (*openFile)(nil)
)

// openDir is the [io/fs.File] returned by Open for a directory: its
// entries, fetched upfront, served through ReadDir following the same
// pagination contract as [os.File.ReadDir].
type openDir struct {
	info    *fileInfo
	entries []fs.DirEntry
	pos     int
}

func (od *openDir) Stat() (fs.FileInfo, error) { return od.info, nil }

func (od *openDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: od.info.name, Err: errors.New("is a directory")}
}

func (od *openDir) Close() error { return nil }

func (od *openDir) ReadDir(n int) ([]fs.DirEntry, error) {
	remaining := len(od.entries) - od.pos
	if n <= 0 {
		res := od.entries[od.pos:]
		od.pos = len(od.entries)
		return res, nil
	}
	if remaining == 0 {
		return nil, io.EOF
	}
	if n > remaining {
		n = remaining
	}
	res := od.entries[od.pos : od.pos+n]
	od.pos += n
	return res, nil
}

var (
	_ fs.File        = (*openDir)(nil)
	_ fs.ReadDirFile = (*openDir)(nil)
)
