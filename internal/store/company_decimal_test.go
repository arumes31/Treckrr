package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

// TestInvoiceDecimalCompatibility pins the pre-upgrade decimal representation:
// changing quoted numbers or trimming would invalidate historical content hashes.
func TestInvoiceDecimalCompatibility(t *testing.T) {
	t.Parallel()
	const snapshot = `{"net":"11.115","vat_rate":"20","vat_amount":"2.22","gross":"13.335",` +
		`"show_vat":true,"tax_mode":"regel","tax_note":"Test",` +
		`"service_from":"2026-10-01T00:00:00Z","service_to":"2026-10-01T00:00:00Z",` +
		`"issuer":{"name":"Test issuer","address":"Test address","tax_id":""},` +
		`"recipient":{"name":"Test recipient","address":"Test address","tax_id":""},` +
		`"lines":[{"date":"2026-10-01T00:00:00Z","label":"Test work","unit":"h",` +
		`"qty":"1.5","unit_price":"7.41","cost":"11.115"}],` +
		`"payment_term_days":14,"skonto_pct":"0","skonto_until":"0001-01-01T00:00:00Z"}`
	day := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	content := invoiceContentFromEntries(
		models.Company{
			Name: "Test issuer", Address: "Test address", TaxMode: "regel",
			VATRate: decimal.RequireFromString("20.0000"), TaxNote: "Test", PaymentTermDays: 14,
		},
		models.Neighbor{Name: "Test recipient", Address: "Test address"},
		[]models.Entry{
			{
				Date: day, TaskLabel: "Test work", Unit: "h",
				Hours: decimal.RequireFromString("1.500"), HourlyRate: decimal.RequireFromString("7.4100"),
				Cost: decimal.RequireFromString("11.1150"),
			},
			{Date: day, Voided: true, Cost: decimal.NewFromInt(999)},
		},
	)
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != snapshot {
		t.Fatalf("invoice snapshot changed:\ngot  %s\nwant %s", encoded, snapshot)
	}
	sum := sha256.Sum256([]byte(snapshot))
	wantHash := hex.EncodeToString(sum[:])
	if content.Hash != wantHash {
		t.Fatalf("invoice content hash = %s, want %s", content.Hash, wantHash)
	}
	var restored models.InvoiceContent
	if err := json.Unmarshal([]byte(snapshot), &restored); err != nil {
		t.Fatal(err)
	}
	if got := invoiceContentHash(restored); got != wantHash {
		t.Fatalf("historical snapshot hash = %s, want %s", got, wantHash)
	}
}

// TestInvoiceDecimalSQLCompatibility covers the string/byte numeric values used
// by SQL scanners without opening a database or converting money through floats.
func TestInvoiceDecimalSQLCompatibility(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{name: "four place amount", input: "11.1150", want: "11.115"},
		{name: "negative credit", input: []byte("-60.0000"), want: "-60"},
		{name: "zero", input: "0.0000", want: "0"},
		{name: "large exact amount", input: "99999999999999.9999", want: "99999999999999.9999"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var amount decimal.NullDecimal
			if err := amount.Scan(test.input); err != nil {
				t.Fatal(err)
			}
			if !amount.Valid || amount.Decimal.String() != test.want {
				t.Fatalf("Scan = %v, want valid %s", amount, test.want)
			}
			value, err := amount.Value()
			if err != nil || value != test.want {
				t.Fatalf("Value = %v, %v; want %s", value, err, test.want)
			}
			if err := amount.Scan(nil); err != nil || amount.Valid {
				t.Fatalf("Scan(NULL) = %v, %v; want invalid", amount, err)
			}
		})
	}
}

// TestInvoiceDecimalDecodeGuards keeps malformed or extreme decimal input from
// becoming valid money when stored snapshots or SQL numeric values are decoded.
func TestInvoiceDecimalDecodeGuards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{name: "large positive exponent", input: "1e10001"},
		{name: "large negative exponent", input: "1e-10001"},
		{name: "misplaced sign", input: ".-5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decimal.NewFromString(test.input); err == nil {
				t.Error("NewFromString accepted unsafe input")
			}
			amount := decimal.NewNullDecimal(decimal.NewFromInt(123))
			if err := amount.Scan(test.input); err == nil || amount.Valid {
				t.Error("failed SQL decoding must return an error and invalidate the previous amount")
			}
			amount = decimal.NewNullDecimal(decimal.NewFromInt(123))
			encoded, err := json.Marshal(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &amount); err == nil || amount.Valid {
				t.Error("failed JSON decoding must return an error and invalidate the previous amount")
			}
		})
	}
}
