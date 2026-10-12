//go:build integration

package server

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestBookingSuccessAcknowledgementIntegration(t *testing.T) {
	e := newItEnv(t)
	form := url.Values{
		"booking_form_version": {"2"}, "booking_kind": {"fixed"}, "booking_direction": {"out"},
		"neighbor_id": {itoa64(e.neighborID)}, "year_id": {itoa64(e.yearID64)},
		"entry_date": {"2026-10-12"}, "task_label": {"Draft acknowledgement"}, "amount": {"12.50"},
		"idempotency_key": {"draft-ack-" + e.uname}, "continue_booking": {"1"},
	}
	post := func() (int, string) {
		t.Helper()
		form.Set("csrf_token", e.csrf("/"))
		req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/entries", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Booking-Response", "json")
		res, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(body)
	}
	for i := 0; i < 2; i++ {
		if status, body := post(); status != 200 || !strings.Contains(body, `"saved":true`) {
			t.Fatalf("attempt %d: %d %s", i, status, body)
		}
	}
	entries, err := e.st.ListNeighborLedger(e.ctx, e.yearID64, e.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Booking != nil && entry.Booking.TaskLabel == "Draft acknowledgement" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate booking: %d", count)
	}
	form.Set("idempotency_key", "draft-invalid-"+e.uname)
	form.Set("amount", "-1")
	if _, body := post(); strings.Contains(body, `"saved":true`) {
		t.Fatal("invalid draft acknowledged")
	}
	// Native form submissions carry the clicked button's name, without the
	// progressive JSON header or any JavaScript-updated hidden field.
	form.Set("idempotency_key", "draft-native-"+e.uname)
	form.Set("amount", "12.50")
	form.Set("booking_direction", "in")
	form.Set("note", "Do not carry this note into a new booking")
	form.Set("continue_booking", "0")
	form.Set("continue_next", "1")
	form.Set("csrf_token", e.csrf("/"))
	res, err := e.client.PostForm(e.srv.URL+"/entries", form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Request.URL.Path != "/neighbors/"+itoa64(e.neighborID) ||
		res.Request.URL.Query().Get("view") != "booking" || res.Request.URL.Query().Get("year") != itoa64(e.yearID64) {
		t.Fatalf("native save-next destination: %d %s", res.StatusCode, res.Request.URL)
	}
	query := res.Request.URL.Query()
	if len(query) != 5 || query.Get("next_kind") != "fixed" || query.Get("next_direction") != "in" ||
		query.Get("next_date") != form.Get("entry_date") {
		t.Fatalf("native continuation leaked or lost defaults: %s", res.Request.URL)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	assertBookingContinuationForm(t, string(body), "fixed", "in", form.Get("entry_date"))
	for _, field := range []string{"amount", "hours", "quantity", "unit_price", "partner_rate", "note", "task_label"} {
		input := regexp.MustCompile(`<input\b[^>]*\bname="` + field + `"[^>]*>`).FindString(string(body))
		if input == "" || !strings.Contains(input, `value=""`) {
			t.Errorf("next booking retained %s: %s", field, input)
		}
	}
	// Ordinary saves still return to history without any continuation defaults.
	form.Del("continue_next")
	form.Set("idempotency_key", "draft-native-normal-"+e.uname)
	form.Set("csrf_token", e.csrf("/"))
	normal, err := e.client.PostForm(e.srv.URL+"/entries", form)
	if err != nil {
		t.Fatal(err)
	}
	defer normal.Body.Close()
	if normal.StatusCode != http.StatusOK || normal.Request.URL.RequestURI() != neighborBookingsURL(e.neighborID, e.yearID64) {
		t.Fatalf("ordinary save destination changed: %d %s", normal.StatusCode, normal.Request.URL)
	}
}

func TestBookingContinuationQueryIntegration(t *testing.T) {
	e := newItEnv(t)
	for _, tc := range []struct {
		name, query, kind, direction, date string
	}{
		{"equipment", "next_kind=equipment&next_direction=in&next_date=2026-08-14", "equipment", "in", "2026-08-14"},
		{"labor", "next_kind=labor&next_direction=in&next_date=2026-08-15", "labor", "in", "2026-08-15"},
		{"quantity", "next_kind=quantity&next_direction=out&next_date=2026-08-16", "quantity", "out", "2026-08-16"},
		{"invalid values", "next_kind=other&next_direction=sideways&next_date=2026-02-30", "equipment", "out", ""},
		{"noncanonical date", "next_date=2026-10-2", "equipment", "out", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.client.Get(e.srv.URL + neighborBookingURL(e.neighborID, e.yearID64) + "&" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("booking form: %d", res.StatusCode)
			}
			date := tc.date
			if date == "" {
				date = time.Now().Format(time.DateOnly)
			}
			assertBookingContinuationForm(t, string(body), tc.kind, tc.direction, date)
		})
	}
}

// assertBookingContinuationForm checks the server-rendered defaults without JavaScript.
func assertBookingContinuationForm(t *testing.T, body, kind, direction, date string) {
	t.Helper()
	for _, pattern := range []string{
		`<option value="` + kind + `" selected>`,
		`<input type="radio" name="booking_direction" value="` + direction + `" checked>`,
		`name="entry_date" value="` + date + `"`,
	} {
		if !strings.Contains(body, pattern) {
			t.Errorf("booking form lacks retained default %s", pattern)
		}
	}
}
