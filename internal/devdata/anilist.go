package devdata

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

// AnilistHandler answers the client's four GraphQL operations from the fixture
// set, so a title can be added and Discovery rendered with no network. It
// discriminates on the operation's variables, which survive edits to the
// selection sets that matching on query text would not: only the paged schedule
// query sends id and page together.
func AnilistHandler(now time.Time) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respond(req.Variables, now))
	})
	return mux
}

func respond(vars map[string]any, now time.Time) map[string]any {
	switch {
	case vars["search"] != nil:
		term, _ := vars["search"].(string)
		return data("Page", map[string]any{"media": searchMedia(term, now)})
	case vars["season"] != nil:
		page, _ := vars["page"].(float64)
		return data("Page", map[string]any{
			"pageInfo": map[string]any{"hasNextPage": false},
			"media":    browseMedia(page, now),
		})
	case vars["id"] != nil && vars["page"] != nil:
		return byID(vars, now, scheduleMedia)
	case vars["id"] != nil:
		return byID(vars, now, titleMedia)
	}
	return graphQLError("devseed stub: unrecognised operation")
}

// byID answers an unseeded id the way AniList does — a GraphQL error rather than
// a null Media — so the client's not-found path is reachable offline.
func byID(vars map[string]any, now time.Time, build func(title, time.Time) map[string]any) map[string]any {
	id, ok := vars["id"].(float64)
	if !ok {
		return graphQLError("devseed stub: id was not a number")
	}
	t, found := findTitle(int64(id))
	if !found {
		return graphQLError("Not Found.")
	}
	return data("Media", build(t, now))
}

func graphQLError(msg string) map[string]any {
	return map[string]any{"errors": []any{map[string]any{"message": msg}}}
}

func data(key string, body any) map[string]any {
	return map[string]any{"data": map[string]any{key: body}}
}

func mediaJSON(t title, now time.Time) map[string]any {
	m := map[string]any{
		"id":           t.providerID,
		"title":        map[string]any{"romaji": t.name, "english": t.name, "native": t.name},
		"description":  "A synthetic title seeded for local development.",
		"format":       providerFormat(t.format),
		"status":       t.status,
		"coverImage":   map[string]any{"large": t.cover},
		"genres":       []string{"Action", "Fantasy"},
		"averageScore": 70 + int(t.providerID%25),
		"studios":      map[string]any{"nodes": []any{map[string]any{"name": "Studio Placeholder"}}},
	}
	// A null episode count is normal operation, so the stub has to be able to
	// publish one (#151).
	if t.episodes > 0 {
		m["episodes"] = t.episodes
	} else {
		m["episodes"] = nil
	}
	if t.year > 0 {
		m["seasonYear"] = t.year
		m["startDate"] = map[string]any{"year": t.year, "month": 4, "day": 5}
	} else {
		m["seasonYear"] = nil
		m["startDate"] = map[string]any{"year": nil, "month": nil, "day": nil}
	}
	if n, at, ok := nextBroadcast(t, now); ok {
		m["nextAiringEpisode"] = map[string]any{"episode": n, "airingAt": at.Unix()}
	} else {
		m["nextAiringEpisode"] = nil
	}
	return m
}

func titleMedia(t title, now time.Time) map[string]any {
	m := mediaJSON(t, now)
	m["airingSchedule"] = map[string]any{
		"pageInfo": map[string]any{"hasNextPage": false},
		"nodes":    scheduleNodes(t, now, false),
	}
	return m
}

func scheduleMedia(t title, now time.Time) map[string]any {
	return map[string]any{
		"airingSchedule": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": false},
			"nodes":    scheduleNodes(t, now, true),
		},
	}
}

// scheduleNodes publishes only the items the fixture dates, which is what makes
// the gap-filled run reproducible through the provider as well as the seeder.
func scheduleNodes(t title, now time.Time, withTimes bool) []any {
	var out []any
	for _, it := range t.items {
		if !it.dated {
			continue
		}
		node := map[string]any{"episode": it.number}
		if withTimes {
			node["airingAt"] = now.Add(it.airsIn).Unix()
		}
		out = append(out, node)
	}
	return out
}

func searchMedia(term string, now time.Time) []any {
	needle := strings.ToLower(strings.TrimSpace(term))
	var out []any
	for _, t := range served() {
		for _, name := range append([]string{t.name}, t.altNames...) {
			if needle == "" || strings.Contains(strings.ToLower(name), needle) {
				out = append(out, mediaJSON(t, now))
				break
			}
		}
	}
	return out
}

// browseMedia answers only the first page: the fixture chart is smaller than one
// page, and hasNextPage false is what stops the client asking for a second.
func browseMedia(page float64, now time.Time) []any {
	if page > 1 {
		return nil
	}
	var out []any
	for _, t := range served() {
		out = append(out, mediaJSON(t, now))
	}
	return out
}

func findTitle(id int64) (title, bool) {
	for _, t := range served() {
		if t.providerID == id {
			return t, true
		}
	}
	return title{}, false
}

func nextBroadcast(t title, now time.Time) (int, time.Time, bool) {
	for _, it := range t.items {
		if it.dated && it.airsIn > 0 {
			return it.number, now.Add(it.airsIn), true
		}
	}
	return 0, time.Time{}, false
}

// providerFormat renders the domain format back in AniList's vocabulary, since
// the fixtures hold the mapped value and the adapter maps it again on the way in.
func providerFormat(f domain.Format) string {
	switch f {
	case domain.FormatMovie:
		return "MOVIE"
	case domain.FormatOVA:
		return "OVA"
	case domain.FormatONA:
		return "ONA"
	case domain.FormatSpecial:
		return "SPECIAL"
	default:
		return "TV"
	}
}
