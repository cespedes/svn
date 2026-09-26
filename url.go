package svn

import (
	"fmt"
	"net/url"
	"strings"
)

// RepoRelativePath returns targetURL's own path relative to rootURL (a
// repository's own root URL, e.g. [Client.Info].URL on the client side,
// or the ReposInfo.URL a [Server]'s Greet callback returned on the
// server side), with no leading or trailing slash, or an error if
// targetURL isn't under that root.
//
// This is what a "diff" command's own "url" parameter (see
// [Server.Diff]) needs turning into a usable path with: unlike "update"
// (see [Server.Update]), which a real client always anchors a fresh
// session at exactly the update target's own parent directory (so
// "update"'s own "target" parameter reliably names only that target's
// bare child name, relative to the session already open), a real "svn
// diff" of a single file reuses an existing, possibly much
// higher-anchored session, while still reporting "target" as only that
// same bare child name -- so using "target" the same way "update"'s own
// works would only ever match a direct child of whatever the session
// happens to be anchored at, silently producing an empty diff for
// anything nested deeper (confirmed against a real "svn diff" of a
// nested file). "url" -- compared against the repository's own root
// via this function -- is what reliably gives the target's real,
// repository-root-relative path instead; a [Server.FinishReport]
// implementation still needs to strip its own session anchor's prefix
// from the result before passing it on to [Server.UpdateEdit] as
// target, the same way it already resolves every other path argument.
func RepoRelativePath(rootURL, targetURL string) (string, error) {
	root, err := url.Parse(rootURL)
	if err != nil {
		return "", fmt.Errorf("svn: RepoRelativePath: parsing root URL %q: %w", rootURL, err)
	}
	target, err := url.Parse(strings.TrimSuffix(targetURL, "/"))
	if err != nil {
		return "", fmt.Errorf("svn: RepoRelativePath: parsing target URL %q: %w", targetURL, err)
	}
	rootPath := strings.TrimSuffix(root.Path, "/")
	if !strings.HasPrefix(target.Path, rootPath) {
		return "", fmt.Errorf("svn: RepoRelativePath: %q is not under repository root %q", targetURL, rootURL)
	}
	return strings.Trim(target.Path[len(rootPath):], "/"), nil
}
