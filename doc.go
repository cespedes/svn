// Package svn provides client and server implementations of the SVN wire
// protocol (ra_svn) -- the protocol used by svnserve and svn+ssh:// URLs,
// as opposed to the HTTP-based (DAV) protocol.
//
// Use [Connect] to start a client, and [Server] to implement a server.
package svn
