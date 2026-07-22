package qbittorrent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/matthewdias/transpondarr/internal/core/download"
)

// Add injects a torrent. The info hash is derived locally (qBittorrent's add
// endpoint does not return it): from the magnet URI, or by hashing the .torrent
// metainfo — fetching it first when only an http(s) URL is supplied. If the
// client is already managing that hash, Add reports AddAlreadyExists without
// re-adding (a re-add would reset the torrent).
func (c *Client) Add(ctx context.Context, opts download.AddOptions) (download.AddResult, error) {
	hash, magnet, content, err := c.resolveAdd(ctx, opts)
	if err != nil {
		return download.AddResult{}, err
	}

	if existing, err := c.Status(ctx, hash); err != nil {
		return download.AddResult{}, err
	} else if len(existing) > 0 {
		return download.AddResult{Hash: hash, Outcome: download.AddAlreadyExists}, nil
	}

	// qBittorrent auto-creates the category named on the add, so we just pass it.
	if magnet != "" {
		_, err = c.qb.AddTorrentFromUrlCtx(ctx, magnet, addOptions(opts))
	} else {
		_, err = c.qb.AddTorrentFromMemoryCtx(ctx, content, addOptions(opts))
	}
	if err != nil {
		return download.AddResult{}, fmt.Errorf("qbittorrent: add: %w", err)
	}
	return download.AddResult{Hash: hash, Outcome: download.AddSuccess}, nil
}

// resolveAdd derives the info hash and decides how to hand the torrent to
// qBittorrent: as a magnet URL (returned in magnet) or as raw .torrent bytes
// (returned in content). Exactly one of the two is non-empty on success.
func (c *Client) resolveAdd(ctx context.Context, opts download.AddOptions) (hash, magnet string, content []byte, err error) {
	switch {
	case opts.Content == nil && opts.URL == "":
		return "", "", nil, fmt.Errorf("qbittorrent: add requires URL or Content")
	case opts.Content == nil && strings.HasPrefix(opts.URL, "magnet:"):
		hash, err = download.InfoHashFromMagnet(opts.URL)
		return hash, opts.URL, nil, err
	case opts.Content != nil:
		hash, err = download.InfoHashFromMeta(opts.Content)
		return hash, "", opts.Content, err
	default:
		// An http(s) URL. Fetch it ourselves so we can hash the metainfo for a
		// deterministic ID; the fetch also transparently handles a redirect to a
		// magnet (common for magnet-only indexers proxied via Prowlarr/Jackett).
		data, mag, ferr := c.fetchTorrent(ctx, opts.URL)
		if ferr != nil {
			return "", "", nil, ferr
		}
		if mag != "" {
			hash, err = download.InfoHashFromMagnet(mag)
			return hash, mag, nil, err
		}
		hash, err = download.InfoHashFromMeta(data)
		return hash, "", data, err
	}
}

// addOptions maps AddOptions to qBittorrent's add form fields.
func addOptions(opts download.AddOptions) map[string]string {
	o := map[string]string{}
	if opts.Category != "" {
		o["category"] = opts.Category
	}
	if opts.SavePath != "" {
		o["savepath"] = opts.SavePath
	}
	if opts.Paused {
		// qBittorrent 5.0 renamed this add param from "paused" to "stopped"; send
		// both so the torrent starts inactive on 4.x and 5.x alike.
		o["paused"] = "true"
		o["stopped"] = "true"
	}
	return o
}

// fetchTorrent downloads a .torrent file. It uses a dedicated client so the
// qBittorrent session cookie is never sent to a third-party indexer host. If the
// URL redirects to a magnet: URI (common for magnet-only indexers proxied through
// Prowlarr/Jackett), it returns that magnet instead of file bytes.
func (c *Client) fetchTorrent(ctx context.Context, rawURL string) (content []byte, magnet string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("qbittorrent: fetch torrent: %w", err)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		// The default client aborts a redirect to a non-http scheme with
		// "unsupported protocol scheme". Instead, capture a magnet Location and
		// stop following, so the caller can add it as a magnet.
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if r.URL.Scheme == "magnet" {
				magnet = r.URL.String()
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return fmt.Errorf("qbittorrent: stopped after 10 redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("qbittorrent: fetch torrent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if magnet != "" {
		return nil, magnet, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("qbittorrent: fetch torrent %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		return nil, "", fmt.Errorf("qbittorrent: read torrent: %w", err)
	}
	return data, "", nil
}

// Status returns the state of the requested hashes (all torrents if none given).
func (c *Client) Status(ctx context.Context, hashes ...string) ([]download.Status, error) {
	lower := make([]string, len(hashes))
	for i, h := range hashes {
		lower[i] = strings.ToLower(h)
	}
	torrents, err := c.qb.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{Hashes: lower})
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: status: %w", err)
	}
	out := make([]download.Status, 0, len(torrents))
	for _, t := range torrents {
		out = append(out, download.Status{
			Hash:        t.Hash,
			Name:        t.Name,
			State:       mapState(string(t.State)),
			Progress:    t.Progress,
			SavePath:    t.SavePath,
			ContentPath: t.ContentPath,
		})
	}
	return out, nil
}

// mapState normalizes qBittorrent's state vocabulary to download.State.
// See https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-(qBittorrent-4.1)
func mapState(s string) download.State {
	switch s {
	case "downloading", "metaDL", "queuedDL", "forcedDL":
		return download.StateDownloading
	case "stalledDL":
		return download.StateStalled
	case "uploading", "queuedUP", "stalledUP", "forcedUP", "pausedUP", "stoppedUP":
		// Download finished (may be seeding, queued to seed, or done).
		return download.StateComplete
	case "checkingDL", "checkingUP", "checkingResumeData", "allocating", "moving":
		return download.StateChecking
	case "pausedDL", "stoppedDL":
		return download.StatePaused
	case "error", "missingFiles":
		return download.StateError
	default:
		return download.StateUnknown
	}
}
