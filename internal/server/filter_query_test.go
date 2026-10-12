package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestFilterQueryBounds checks decoded values so URL encoding cannot hide an
// unbounded value, and ordinary filters survive pager/export link rebuilding.
func TestFilterQueryBounds(t *testing.T) {
	t.Parallel()
	for _, builder := range []struct {
		name string
		keys []string
		link func(*http.Request) string
	}{
		{name: "audit", keys: []string{"q", "action", "username", "from", "to"}, link: auditFilterQuery},
		{name: "statistics", keys: []string{"from", "to"}, link: statsPeriodQuery},
		{
			name: "bookings",
			keys: []string{"from", "to", "task", "unit", "voided", "neighbor_id", "sort", "dir", "direction", "kind"},
			link: func(r *http.Request) string { return entryListURL(r, 42, 2, "") },
		},
	} {
		t.Run(builder.name, func(t *testing.T) {
			for _, tc := range []struct{ name, value, want string }{
				{name: "ordinary", value: "  2026-10-12  ", want: "2026-10-12"},
				{name: "URL metacharacters", value: "<x>&50%+#", want: "<x>&50%+#"},
				{name: "boundary", value: strings.Repeat("a", maxNameLen), want: strings.Repeat("a", maxNameLen)},
				{name: "oversized ASCII", value: strings.Repeat("x", maxNameLen+200), want: strings.Repeat("x", maxNameLen)},
				{name: "oversized Unicode", value: strings.Repeat("ä", maxNameLen+200), want: strings.Repeat("ä", maxNameLen)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					values := url.Values{"unrecognized": {"must-not-propagate"}}
					for _, key := range builder.keys {
						values.Set(key, tc.value)
					}
					r := httptest.NewRequest(http.MethodGet, "/?"+values.Encode(), nil)
					link := builder.link(r)
					query := strings.TrimPrefix(link, "&")
					if _, after, found := strings.Cut(link, "?"); found {
						query = after
					}
					got, err := url.ParseQuery(query)
					if err != nil {
						t.Fatal(err)
					}
					for _, key := range builder.keys {
						value := got.Get(key)
						if value != tc.want || !utf8.ValidString(value) {
							t.Errorf("%s = %q, want %q (valid UTF-8)", key, value, tc.want)
						}
					}
					if got.Get("unrecognized") != "" {
						t.Error("unknown query parameter propagated")
					}
				})
			}
		})
	}
}
