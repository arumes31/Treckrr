//go:build integration

package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCoreOperationScalingIntegration is an opt-in measurement harness, not a
// machine-dependent speed gate. It exercises real handlers/templates/Postgres
// with deterministic small/large synthetic accounts in the test fixture only.
func TestCoreOperationScalingIntegration(t *testing.T) {
	if os.Getenv("TRECKRR_PERF_TESTS") != "1" {
		t.Skip("set TRECKRR_PERF_TESTS=1 against a disposable TEST_DATABASE_URL to measure core operations")
	}
	t.Logf("PERF_SOURCE %s", os.Getenv("TRECKRR_TEST_SOURCE"))
	for _, size := range []int{25, 1000} {
		t.Run(fmt.Sprintf("entries_%d", size), func(t *testing.T) {
			e := newItEnv(t)
			// The larger invoice exceeds the recipient-UID threshold. Supply
			// synthetic complete billing details rather than bypass validation.
			if err := e.st.UpdateNeighbor(e.ctx, e.neighborID, "IT-Nachbar "+e.uname, "",
				"Feldweg 1, 4710 Testdorf", "ATU00000001", "", "", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.ExecContext(e.ctx, `INSERT INTO entries
				(neighbor_id,billing_year_id,entry_date,task_label,tractor_label,load_label,hours,hourly_rate,cost,unit,quantity,unit_price)
				SELECT $1,$2,'2026-03-02','Synthetic performance booking','Synthetic tractor','mittel',1,46,46,'h',1,46 FROM generate_series(1,$3)`, e.neighborID, e.yearID64, size); err != nil {
				t.Fatal(err)
			}
			csrf := e.csrf("/")
			e.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			account := fmt.Sprintf("/neighbors/%d?year=%d", e.neighborID, e.yearID64)
			for _, op := range []struct{ name, path string }{
				{"dashboard", fmt.Sprintf("/?year=%d", e.yearID64)},
				{"neighbor_account", account},
				{"booking_save", "/entries"},
				{"pdf", fmt.Sprintf("/neighbors/%d/beleg.pdf?year=%d", e.neighborID, e.yearID64)},
				{"export", fmt.Sprintf("/export/neighbor/%d?year=%d", e.neighborID, e.yearID64)},
			} {
				// The PDF route serves only a frozen invoice. Create that fixture
				// after the save samples (issuance locks the account), outside the
				// measured requests so we time rendering rather than issuance.
				if op.name == "pdf" {
					iv, err := e.st.IssueInvoice(e.ctx, e.yearID64, e.neighborID, e.year, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC))
					if err != nil {
						t.Fatalf("issue synthetic invoice: %v", err)
					}
					if iv.Content == nil || len(iv.Content.Lines) != size+3 {
						t.Fatal("synthetic invoice does not contain every seeded/saved booking")
					}
				}
				for sample := 0; sample < 3; sample++ {
					var req *http.Request
					var err error
					var bookingKey string
					var rowsBefore int
					if op.name == "booking_save" {
						bookingKey = fmt.Sprintf("perf-%s-%d", e.uname, sample)
						if err := e.pool.QueryRowContext(e.ctx, `SELECT count(*) FROM entries WHERE billing_year_id=$1 AND neighbor_id=$2`, e.yearID64, e.neighborID).Scan(&rowsBefore); err != nil {
							t.Fatal(err)
						}
						form := url.Values{"csrf_token": {csrf}, "year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)}, "gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-03-02"}, "hours": {"1"}, "unit": {"h"}, "idempotency_key": {bookingKey}}
						req, err = http.NewRequestWithContext(e.ctx, http.MethodPost, e.srv.URL+op.path, strings.NewReader(form.Encode()))
						if err == nil {
							req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						}
					} else {
						req, err = http.NewRequestWithContext(e.ctx, http.MethodGet, e.srv.URL+op.path, nil)
					}
					if err != nil {
						t.Fatal(err)
					}
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					poolBefore := e.pool.Stats()
					start := time.Now()
					res, err := e.client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, readErr := io.ReadAll(res.Body)
					closeErr := res.Body.Close()
					took := time.Since(start)
					runtime.ReadMemStats(&after)
					poolAfter := e.pool.Stats()
					if readErr != nil || closeErr != nil {
						t.Fatalf("read=%v close=%v", readErr, closeErr)
					}
					want := http.StatusOK
					if op.name == "booking_save" {
						want = http.StatusSeeOther
					}
					if res.StatusCode != want {
						t.Fatalf("%s status=%d want=%d location=%q", op.name, res.StatusCode, want, res.Header.Get("Location"))
					}
					if op.name == "pdf" && (res.Header.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(string(body), "%PDF-")) {
						t.Fatal("PDF sample did not return a PDF artifact")
					}
					if op.name == "booking_save" {
						var rowsAfter, stored int
						if err := e.pool.QueryRowContext(e.ctx, `SELECT count(*),count(*) FILTER(WHERE idempotency_key=$3) FROM entries WHERE billing_year_id=$1 AND neighbor_id=$2`, e.yearID64, e.neighborID, bookingKey).Scan(&rowsAfter, &stored); err != nil {
							t.Fatal(err)
						}
						if rowsAfter != rowsBefore+1 || stored != 1 {
							t.Fatalf("save was not committed: before=%d after=%d matching_key=%d", rowsBefore, rowsAfter, stored)
						}
					}
					t.Logf("PERF rows=%d operation=%s sample=%d wall_ms=%.3f allocated_bytes=%d db_pool_waits=%d response_bytes=%d", size, op.name, sample+1, float64(took)/float64(time.Millisecond), after.TotalAlloc-before.TotalAlloc, poolAfter.WaitCount-poolBefore.WaitCount, len(body))
				}
			}
		})
	}
}
