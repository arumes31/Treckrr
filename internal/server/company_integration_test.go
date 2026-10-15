//go:build integration

package server

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"golang.org/x/net/html"

	"github.com/d0linger/treckrr/internal/models"
)

// TestCompanyUnchangedFormPreservesSettingsIntegration posts the actual rendered
// controls back unchanged, including readonly inputs and collapsed sections.
func TestCompanyUnchangedFormPreservesSettingsIntegration(t *testing.T) {
	e := newItEnv(t)
	original, err := e.st.GetCompany(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.st.UpdateCompany(e.ctx, original); err != nil {
			t.Errorf("restore company fixture: %v", err)
		}
	})
	company := models.Company{
		Name: "Synthetic settings & preservation", Address: "Testweg 7\n4710 Testdorf",
		TaxID: "ATU00000001", TaxNote: "Synthetic notice <unchanged>", TaxMode: "regel",
		VATRate: decimal.NewFromInt(20), IBAN: "AT611904300234573201", PaymentTermDays: 23,
		DunningFee1: decimal.RequireFromString("1234.56"), DunningFee2: decimal.NewFromInt(1000),
		DunningGraceDays: 19, SkontoPct: decimal.RequireFromString("2.5"), SkontoDays: 8,
		InvoicePrefix: "TEST", InvoiceStart: 4321, SmallBusinessLimit: decimal.NewFromInt(55000),
		TravelFlat: decimal.RequireFromString("1000000.01"), TravelPerKm: decimal.RequireFromString("1234.56"),
		MailSignature: "Synthetic signature\nLine & two", MailCC: "synthetic@example.invalid",
		EInvoiceStreet: "Testweg 7", EInvoiceZIP: "4710", EInvoiceTown: "Testdorf", EInvoiceCountryCode: "AT",
	}
	if err := e.st.UpdateCompany(e.ctx, company); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := e.pool.QueryRowContext(e.ctx, `SELECT to_jsonb(c)::text FROM company c WHERE id=1`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	// Compare the persisted baseline, after PostgreSQL has normalized numeric
	// scales, rather than comparing a decimal's internal Go representation.
	before := snapshot()
	form := renderedCompanyForm(t, e.get("/admin/company"))
	if len(form) != 25 || form.Get("csrf_token") == "" {
		t.Fatalf("rendered form has %d fields; want all 24 settings and a CSRF token", len(form))
	}
	response, err := e.client.PostForm(e.srv.URL+"/admin/company", form)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Betriebsdaten gespeichert.") {
		t.Fatalf("unchanged settings were not saved: status=%d", response.StatusCode)
	}
	if after := snapshot(); after != before {
		t.Fatalf("unchanged form modified saved company settings:\nbefore: %s\nafter:  %s", before, after)
	}
}

// renderedCompanyForm models the successful controls used by this form. HTML
// parsing preserves decoded textarea content and the selected taxation option.
func renderedCompanyForm(t *testing.T, page string) url.Values {
	t.Helper()
	var source string
	for _, candidate := range formRe.FindAllString(page, -1) {
		if strings.Contains(candidate, `action="/admin/company"`) {
			source = candidate
			break
		}
	}
	if source == "" {
		t.Fatal("company form is missing")
	}
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(node *html.Node, key string) (string, bool) {
		for _, attribute := range node.Attr {
			if attribute.Key == key {
				return attribute.Val, true
			}
		}
		return "", false
	}
	values := url.Values{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if _, disabled := attr(node, "disabled"); disabled {
			return
		}
		if name, _ := attr(node, "name"); name != "" {
			switch node.Data {
			case "input":
				value, _ := attr(node, "value")
				values.Add(name, value)
			case "textarea":
				var value strings.Builder
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					value.WriteString(child.Data)
				}
				values.Add(name, value.String())
			case "select":
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if _, selected := attr(child, "selected"); selected {
						value, _ := attr(child, "value")
						values.Add(name, value)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return values
}

// TestCompanyDecimalLimitsIntegration verifies accepted boundary values and
// that rejecting one oversized field leaves every saved setting untouched.
func TestCompanyDecimalLimitsIntegration(t *testing.T) {
	e := newItEnv(t)
	fields := []string{"dunning_fee_1", "dunning_fee_2", "vat_rate", "travel_flat", "travel_per_km", "small_business_limit"}
	for _, tc := range []struct{ name, value, want string }{
		{name: "empty", value: "", want: "0.00"},
		{name: "normal", value: "2,5", want: "2.50"},
		{name: "at limit", value: strings.Repeat("0", 29) + "2,5", want: "2.50"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"name": {"Limit test"}, "tax_mode": {"regel"}}
			for _, field := range fields {
				form.Set(field, tc.value)
			}
			if body := e.post("/admin/company", form); !strings.Contains(body, "Betriebsdaten gespeichert.") {
				t.Fatal("valid settings were not accepted")
			}
			got, err := e.st.GetCompany(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			for i, value := range []decimal.Decimal{
				got.DunningFee1, got.DunningFee2, got.VATRate,
				got.TravelFlat, got.TravelPerKm, got.SmallBusinessLimit,
			} {
				if value.StringFixed(2) != tc.want {
					t.Errorf("%s = %s, want %s", fields[i], value, tc.want)
				}
			}
		})
	}
	for _, field := range fields {
		t.Run(field+" over limit", func(t *testing.T) {
			body := e.post("/admin/company", url.Values{
				"name": {"must not save"}, field: {strings.Repeat("1", 33)},
			})
			if !strings.Contains(body, "höchstens 32 Zeichen") {
				t.Fatal("missing length validation")
			}
			got, err := e.st.GetCompany(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != "Limit test" {
				t.Error("rejected submission changed company name")
			}
			for _, value := range []decimal.Decimal{
				got.DunningFee1, got.DunningFee2, got.VATRate,
				got.TravelFlat, got.TravelPerKm, got.SmallBusinessLimit,
			} {
				if value.StringFixed(2) != "2.50" {
					t.Error("rejected submission changed saved decimal settings")
				}
			}
		})
	}
}

func TestCompanyRejectsInvalidSkontoIntegration(t *testing.T) {
	e := newItEnv(t)
	c, err := e.st.GetCompany(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.SkontoPct = decimal.NewFromInt(2)
	if err := e.st.UpdateCompany(e.ctx, c); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"15", "-1", "not-a-number", "1e9", strings.Repeat("9", 33)} {
		t.Run(value, func(t *testing.T) {
			body := e.post("/admin/company", url.Values{"name": {"must not save"}, "skonto_pct": {value}})
			if !strings.Contains(body, "Skonto muss zwischen 0 und 10 % liegen.") {
				t.Error("missing validation message")
			}
			got, err := e.st.GetCompany(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != c.Name || !got.SkontoPct.Equal(c.SkontoPct) {
				t.Error("invalid submission modified the company")
			}
		})
	}
	for _, value := range []string{"0", "10", "2,5", ""} {
		e.post("/admin/company", url.Values{"name": {c.Name}, "skonto_pct": {value}})
		got, err := e.st.GetCompany(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := decimal.Zero
		if value != "" {
			want = decimal.RequireFromString(strings.ReplaceAll(value, ",", "."))
		}
		if !got.SkontoPct.Equal(want) {
			t.Errorf("valid Skonto %q stored as %s", value, got.SkontoPct)
		}
	}
}
