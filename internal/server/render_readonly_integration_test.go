//go:build integration

package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/d0linger/treckrr/internal/models"
)

// TestReadOnlyRenderingAndMutationGuardIntegration checks the real middleware
// and rendered pages together, using only the isolated integration fixture.
func TestReadOnlyRenderingAndMutationGuardIntegration(t *testing.T) {
	e := newItEnv(t)
	yid, nid := itoa64(e.yearID64), itoa64(e.neighborID)
	e.post("/entries", url.Values{
		"year_id": {yid}, "neighbor_id": {nid}, "gespann_id": {itoa64(e.gespannID)},
		"entry_date": {"2026-04-01"}, "hours": {"2"}, "unit": {"h"},
	})
	entries, err := e.st.ListEntries(e.ctx, e.neighborID, e.yearID64)
	if err != nil || len(entries) != 1 {
		t.Fatalf("booking fixture: count=%d err=%v", len(entries), err)
	}
	before := entries[0]
	viewerName := e.uname + "_viewer"
	if _, err := e.st.CreateUser(e.ctx, viewerName, e.adminPass, models.RoleViewer); err != nil {
		t.Fatal(err)
	}
	// Login creates immutable audit rows. Reuse the fixture's transactional
	// retention cleanup instead of deleting their referenced user directly.
	t.Cleanup(func() { e.purge(viewerName) })
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	viewer := &http.Client{Jar: jar}
	get := func(t *testing.T, path string) string {
		t.Helper()
		resp, err := viewer.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status=%d err=%v", path, resp.StatusCode, err)
		}
		return string(body)
	}
	token := func(page string) string {
		t.Helper()
		match := csrfRe.FindStringSubmatch(page)
		if len(match) != 2 {
			t.Fatal("rendered page has no CSRF token")
		}
		return match[1]
	}
	resp, err := viewer.PostForm(e.srv.URL+"/login", url.Values{
		"username": {viewerName}, "password": {e.adminPass}, "csrf_token": {token(get(t, "/login"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	viewer.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	profile := get(t, "/profile")
	if !strings.Contains(profile, "Nur-Lesen") {
		t.Fatal("viewer login did not establish a read-only session")
	}
	csrf := token(profile)
	for _, path := range []string{
		"/?year=" + yid, "/neighbors", "/years", "/bases", "/personen",
		"/prices?base=" + itoa64(e.baseID64) + "&mode=edit&section=machines",
		"/gespanne?base=" + itoa64(e.baseID64), "/recurring", "/entries/import?year=" + yid,
		"/payments/import", "/buchungen?year=" + yid, "/years/" + yid + "/abschluss",
		"/years/" + yid + "/issue-all", "/neighbors/" + nid + "?year=" + yid + "&view=booking",
		"/neighbors/" + nid + "?year=" + yid + "&view=payments",
		fmt.Sprintf("/entries/%d/edit", before.ID),
	} {
		t.Run(path, func(t *testing.T) {
			page := get(t, path)
			if !strings.Contains(page, "data-readonly-notice") {
				t.Error("read-only page lacks a permission explanation")
			}
			for _, form := range formRe.FindAllString(page, -1) {
				if strings.Contains(form, `action="/logout"`) || strings.Contains(form, `action="/account/`) {
					continue
				}
				if strings.Contains(form, `type="submit"`) {
					t.Errorf("read-only page exposes a POST submit control: %.180s", form)
				}
			}
		})
	}
	account := get(t, "/neighbors/"+nid+"?year="+yid)
	if !strings.Contains(account, "92,00") || !strings.Contains(account, "/export/neighbor/"+nid) {
		t.Fatal("read-only account lost the original amount or its export")
	}
	for _, path := range []string{"/entries", fmt.Sprintf("/entries/%d/update", before.ID), "/neighbors/" + nid + "/payments"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.srv.URL+path, strings.NewReader(url.Values{
			"year_id": {yid}, "neighbor_id": {nid}, "hours": {"99"}, "amount": {"99"},
		}.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("X-Offline-Replay", "1")
		res, err := viewer.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("viewer POST %s: status=%d, want 403", path, res.StatusCode)
		}
		if strings.TrimSpace(string(body)) != readOnlyReplayMsg {
			t.Errorf("viewer POST %s: want role rejection after valid CSRF, got %q", path, body)
		}
	}
	after, err := e.st.ListEntries(e.ctx, e.neighborID, e.yearID64)
	if err != nil || len(after) != 1 || after[0].ID != before.ID || !after[0].Cost.Equal(before.Cost) || !after[0].Hours.Equal(before.Hours) || !after[0].HourlyRate.Equal(before.HourlyRate) {
		t.Fatalf("read-only workflow changed the stored booking: %+v, err=%v", after, err)
	}
	var payments int
	if err := e.pool.QueryRowContext(e.ctx, `SELECT count(*) FROM payments WHERE neighbor_id=$1 AND billing_year_id=$2`, e.neighborID, e.yearID64).Scan(&payments); err != nil || payments != 0 {
		t.Fatalf("read-only workflow created payments: count=%d err=%v", payments, err)
	}
}
