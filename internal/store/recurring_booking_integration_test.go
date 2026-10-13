//go:build integration

package store_test

import (
	"fmt"
	"testing"
	"time"
)

// Persisted companion and companions snapshots deliberately handle deleted
// people differently. Both runners must preserve those formats without rewriting
// the template or changing its agreed prices.
func TestRecurringPeopleCompatibilityIntegration(t *testing.T) {
	for _, runner := range []string{"scheduled", "run now"} {
		t.Run(runner, func(t *testing.T) {
			for _, name := range []string{
				"no people", "legacy live", "legacy missing", "list snapshots", "empty list", "null list",
			} {
				t.Run(name, func(t *testing.T) {
					ctx := t.Context()
					today := time.Now().Format("2006-01-02")
					st, pool, yearID, neighborID := scratchBookingFixtureYear(t, time.Now().Year())
					liveID, err := st.CreatePerson(ctx, "Current master name", dec("99"), "")
					if err != nil {
						t.Fatal(err)
					}
					goneID, err := st.CreatePerson(ctx, "Removed master name", dec("99"), "")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := pool.ExecContext(ctx, `DELETE FROM persons WHERE id=$1`, goneID); err != nil {
						t.Fatal(err)
					}
					type expectedHelper struct {
						personID int64
						label    string
						cost     string
					}
					helpers := []expectedHelper{}
					var primaryID int64
					extra := ""
					liveCompanion := fmt.Sprintf(`{"person_id":%d,"name":"Frozen live","hours":"2","rate":"12.345"}`, liveID)
					switch name {
					case "legacy live", "null list":
						extra = fmt.Sprintf(`,"person_id":%d,"companion":%s`, liveID, liveCompanion)
						if name == "null list" {
							extra += `,"companions":null`
						}
						primaryID = liveID
						helpers = append(helpers, expectedHelper{personID: liveID, label: "Frozen live", cost: "24.69"})
					case "legacy missing":
						extra = fmt.Sprintf(`,"person_id":%d,`+
							`"companion":{"person_id":%d,"name":"Frozen gone","rate":"12.345"}`, goneID, goneID)
					case "list snapshots":
						extra = fmt.Sprintf(`,"person_id":%d,"companions":[%s,`+
							`{"person_id":%d,"name":"Frozen gone","hours":"1","rate":"12.345"},`+
							`{"person_id":0,"name":"Free text","hours":"3","rate":"7.125"}]`,
							goneID, liveCompanion, goneID)
						helpers = append(helpers,
							expectedHelper{personID: liveID, label: "Frozen live", cost: "24.69"},
							expectedHelper{label: "Frozen gone", cost: "12.35"},
							expectedHelper{label: "Free text", cost: "21.38"})
					case "empty list":
						extra = `,"companions":[],"companion":` + liveCompanion
					}
					raw := `{"unit":"h","hours":"2","hourly_rate":"40","cost":"80",` +
						`"task_label":"Frozen recurring","person_name":"Frozen primary"` + extra + `}`
					var ruleID int64
					if err := pool.QueryRowContext(ctx, `INSERT INTO recurring_entries
						(neighbor_id,template,interval_kind,next_run) VALUES ($1,$2::jsonb,'weekly',$3) RETURNING id`,
						neighborID, raw, today).Scan(&ruleID); err != nil {
						t.Fatal(err)
					}
					var before string
					if err := pool.QueryRowContext(ctx, `SELECT template::text FROM recurring_entries WHERE id=$1`, ruleID).
						Scan(&before); err != nil {
						t.Fatal(err)
					}
					for run := range 2 {
						if runner == "scheduled" {
							created, err := st.RunDueRecurring(ctx)
							if err != nil || created != 1-run {
								t.Fatalf("scheduled run %d: created=%d err=%v", run, created, err)
							}
						} else {
							id, booked, err := st.RunRecurringNow(ctx, ruleID)
							if err != nil || !booked || (id > 0) != (run == 0) {
								t.Fatalf("run now %d: id=%d booked=%v err=%v", run, id, booked, err)
							}
						}
					}
					var mainID int64
					key := fmt.Sprintf("recur:%d:%s", ruleID, today)
					if err := pool.QueryRowContext(ctx, `SELECT id FROM entries WHERE idempotency_key=$1`, key).
						Scan(&mainID); err != nil {
						t.Fatal(err)
					}
					entry, err := st.GetEntry(ctx, mainID)
					if err != nil {
						t.Fatal(err)
					}
					if entry.BillingYearID != yearID || entry.Cost.StringFixed(2) != "80.00" || entry.PersonName != "Frozen primary" {
						t.Fatalf("changed primary snapshot: %+v", entry)
					}
					if primaryID == 0 && entry.PersonID != nil || primaryID > 0 && (entry.PersonID == nil || *entry.PersonID != primaryID) {
						t.Fatalf("primary person=%v, want %d", entry.PersonID, primaryID)
					}
					got, err := st.EntryCompanions(ctx, mainID)
					if err != nil || len(got) != len(helpers) {
						t.Fatalf("helpers=%+v err=%v, want %d", got, err, len(helpers))
					}
					for i, want := range helpers {
						if got[i].TaskLabel != "Mannstunden "+want.label || got[i].Cost.StringFixed(2) != want.cost {
							t.Errorf("helper %d snapshot=%+v, want %+v", i, got[i], want)
						}
						if want.personID == 0 && got[i].PersonID != nil || want.personID > 0 && (got[i].PersonID == nil || *got[i].PersonID != want.personID) {
							t.Errorf("helper %d person=%v, want %d", i, got[i].PersonID, want.personID)
						}
					}
					var after string
					if err := pool.QueryRowContext(ctx, `SELECT template::text FROM recurring_entries WHERE id=$1`, ruleID).
						Scan(&after); err != nil {
						t.Fatal(err)
					}
					if before != after {
						t.Fatal("running the rule changed its persisted template")
					}
				})
			}
		})
	}
}
