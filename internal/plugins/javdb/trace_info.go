package javdb

import (
	"net/url"
	"regexp"
	"strings"
)

var loggedHost = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)

// Only fixed endpoint families and the hostname appear in diagnostics.
func traceEndpoint(raw string) (host, route string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "other"
	}
	host = u.Hostname()
	if !loggedHost.MatchString(host) {
		host = "other"
	}
	p := u.Path
	for _, marker := range []string{"/v1/", "/v2/", "/v4/"} {
		if at := strings.Index(p, marker); at >= 0 {
			p = p[at:]
			break
		}
	}
	switch {
	case p == "/v1/movies/latest", p == "/v2/search", p == "/v1/movies/tags", p == "/v2/tags",
		p == "/v1/rankings", p == "/v1/rankings/playback", p == "/v1/actors/recommend",
		p == "/v1/actors", p == "/v1/series", p == "/v1/makers":
		return host, p
	case strings.HasPrefix(p, "/v4/movies/"):
		return host, "/v4/movies/{id}"
	case strings.HasPrefix(p, "/v1/movies/") && strings.HasSuffix(p, "/magnets"):
		return host, "/v1/movies/{id}/magnets"
	case strings.HasPrefix(p, "/v1/actors/") || strings.HasPrefix(p, "/v1/series/") || strings.HasPrefix(p, "/v1/makers/"):
		return host, "/v1/{entity}/{id}"
	case strings.HasSuffix(p, ".json") && strings.Contains(p, "/top250/"):
		return host, "/top250/{type}.json"
	}
	return host, "other"
}
