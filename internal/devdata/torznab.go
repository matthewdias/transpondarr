package devdata

import (
	"encoding/xml"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"time"
)

// TorznabHandler serves canned Torznab XML for the seeded titles, driven by the
// query so a search returns releases covering that title's items.
func TorznabHandler(now time.Time, rngSeed int64) http.Handler {
	entries := buildEntries(now, rngSeed)
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		term := strings.TrimSpace(r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		body, err := renderFeed(matching(entries, term))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	})
	return mux
}

// feedEntry is one canned release plus the title it belongs to, which is what
// makes a search answerable without re-deriving the fixture set per request.
type feedEntry struct {
	titleNames []string
	release    string
	group      string
	hash       string
	size       int64
	seeders    int
	published  time.Time
}

func buildEntries(now time.Time, rngSeed int64) []feedEntry {
	rng := rand.New(rand.NewPCG(uint64(rngSeed)+11, uint64(rngSeed)+13)) //nolint:gosec // fixture variety, not security
	var out []feedEntry
	for _, t := range served() {
		names := append([]string{t.name}, t.altNames...)
		for i, rel := range t.releases {
			size := int64(len(rel.covers)) * (900 + rng.Int64N(700)) << 20
			out = append(out, feedEntry{
				titleNames: names,
				release:    rel.title,
				group:      rel.group,
				hash:       fmt.Sprintf("%040x", uint64(t.providerID)<<20|uint64(i+1)),
				size:       size,
				seeders:    1 + int(rng.Int64N(220)),
				published:  now.Add(-time.Duration(rel.ageDays)*day - time.Duration(i)*time.Hour),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].published.After(out[j].published) })
	return out
}

// matching answers an empty term with everything, which is what the recent feed
// is: a search with no term.
func matching(entries []feedEntry, term string) []feedEntry {
	if term == "" {
		return entries
	}
	needle := strings.ToLower(term)
	var out []feedEntry
	for _, e := range entries {
		for _, name := range e.titleNames {
			if strings.Contains(needle, strings.ToLower(name)) || strings.Contains(strings.ToLower(name), needle) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

type feedXML struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel channelXML `xml:"channel"`
}

type channelXML struct {
	Title string    `xml:"title"`
	Items []itemXML `xml:"item"`
}

type itemXML struct {
	Title     string    `xml:"title"`
	GUID      string    `xml:"guid"`
	PubDate   string    `xml:"pubDate"`
	Size      int64     `xml:"size"`
	Enclosure encXML    `xml:"enclosure"`
	Attrs     []attrXML `xml:"attr"`
}

type encXML struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type attrXML struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func renderFeed(entries []feedEntry) ([]byte, error) {
	feed := feedXML{Version: "2.0", Channel: channelXML{Title: "devseed torznab"}}
	for _, e := range entries {
		feed.Channel.Items = append(feed.Channel.Items, itemXML{
			Title:   e.release,
			GUID:    "devseed:" + e.hash,
			PubDate: e.published.UTC().Format(time.RFC1123Z),
			Size:    e.size,
			Enclosure: encXML{
				URL:    "magnet:?xt=urn:btih:" + e.hash + "&dn=" + strings.ReplaceAll(e.release, " ", "+"),
				Length: e.size,
				Type:   "application/x-bittorrent",
			},
			Attrs: []attrXML{
				{Name: "seeders", Value: fmt.Sprint(e.seeders)},
				{Name: "peers", Value: fmt.Sprint(e.seeders * 2)},
				{Name: "infohash", Value: e.hash},
			},
		})
	}
	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render torznab feed: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}
