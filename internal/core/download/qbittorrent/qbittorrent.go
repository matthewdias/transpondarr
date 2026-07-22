// Package qbittorrent implements the download.Client interface against
// qBittorrent's WebUI API by wrapping the maintained autobrr/go-qbittorrent
// client, which owns the login/CSRF/session handshake and cross-version quirks
// (and re-logins automatically on session expiry).
//
// Transpondarr keeps only the app-specific glue: local info-hash derivation for a
// deterministic ID before the add (qBittorrent's add endpoint does not return the
// hash), an idempotent add that won't clobber an existing torrent, and mapping
// qBittorrent's state vocabulary to download.State.
package qbittorrent

import (
	"context"
	"strings"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/matthewdias/transpondarr/internal/core/download"
)

// Client adapts autobrr/go-qbittorrent to the download.Client interface.
type Client struct {
	qb *qbt.Client
}

// New constructs a qBittorrent client. baseURL is the WebUI root, e.g.
// "http://localhost:8080".
func New(baseURL, username, password string) *Client {
	return &Client{qb: qbt.NewClient(qbt.Config{
		Host:     strings.TrimRight(baseURL, "/"),
		Username: username,
		Password: password,
		Timeout:  30,
	})}
}

func (c *Client) Name() string { return "qbittorrent" }

var _ download.Client = (*Client)(nil)

// Test verifies connectivity and credentials by logging in. The other operations
// authenticate lazily — autobrr establishes and refreshes the session as needed.
func (c *Client) Test(ctx context.Context) error {
	return c.qb.LoginCtx(ctx)
}
