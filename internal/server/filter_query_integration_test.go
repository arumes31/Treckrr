//go:build integration

package server

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestFilterPagesBoundReflectedInput exercises the authenticated HTTP routes,
// including return links in each booking row rather than only pager builders.
func TestFilterPagesBoundReflectedInput(t *testing.T) {
	e := newItEnv(t)
	e.post("/entries", url.Values{
		"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)},
		"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-04-01"},
		"hours": {"2"}, "unit": {"h"},
	})
	oversized := strings.Repeat("x", maxNameLen+200)
	for _, path := range []string{"/admin/audit", "/stats", "/buchungen"} {
		t.Run(path, func(t *testing.T) {
			q := url.Values{
				"year":        {strconv.FormatInt(e.yearID64, 10)},
				"from":        {oversized},
				"to":          {oversized},
				"neighbor_id": {oversized},
				"dir":         {oversized},
			}
			body := e.get(path + "?" + q.Encode())
			if path == "/buchungen" && !strings.Contains(body, `name="return_to"`) {
				t.Fatal("booking row with return target was not rendered")
			}
			if strings.Contains(body, oversized) {
				t.Error("page reflected an unbounded query value")
			}
			if !strings.Contains(body, strings.Repeat("x", maxNameLen)) {
				t.Error("page discarded the bounded filter instead of displaying it")
			}
		})
	}
}
