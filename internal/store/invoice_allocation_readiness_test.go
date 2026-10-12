package store

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func readinessTestInvoice() readinessInvoice {
	return readinessInvoice{
		allocationInvoice: allocationTestInvoice(1, "100"),
		account:           accountKey{yearID: 1, neighborID: 2}, kind: "invoice", hasGross: true,
	}
}

func readinessTestPayment() readinessPayment {
	return readinessPayment{
		allocationPayment: allocationTestPayment(1, "25", 0),
		account:           accountKey{yearID: 1, neighborID: 2},
	}
}

func TestAllocationReadiness(t *testing.T) {
	tests := []struct {
		name   string
		change func(*[]readinessInvoice, *[]readinessPayment, *[]readinessLedger)
		want   string
	}{
		{name: "valid single invoice"},
		{name: "missing frozen gross", change: func(iv *[]readinessInvoice, _ *[]readinessPayment, _ *[]readinessLedger) {
			(*iv)[0].hasGross = false
		}, want: "missing_frozen_gross"},
		{name: "zero document date", change: func(iv *[]readinessInvoice, _ *[]readinessPayment, _ *[]readinessLedger) {
			(*iv)[0].IssuedOn = time.Time{}
		}, want: "invalid_document_date"},
		{name: "fractional payment", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			(*pay)[0].Amount = decimal.RequireFromString("9.995")
		}, want: "legacy_fractional_payment"},
		{name: "fractional reversed cash remains unsupported", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			(*pay)[0].Amount = decimal.RequireFromString("9.995")
			r := (*pay)[0]
			r.ID, r.ReversalOfID, r.Amount = 2, 1, r.Amount.Neg()
			*pay = append(*pay, r)
		}, want: "legacy_fractional_payment"},
		{name: "fractional ledger net", change: func(_ *[]readinessInvoice, _ *[]readinessPayment, led *[]readinessLedger) {
			(*led)[0].net = decimal.RequireFromString("-0.005")
		}, want: "legacy_fractional_ledger_net"},
		{name: "zero payment date", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			(*pay)[0].PaidOn = time.Time{}
		}, want: "invalid_payment_date"},
		{name: "unlinked negative payment", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			(*pay)[0].Amount = decimal.NewFromInt(-25)
		}, want: "invalid_payment_sign"},
		{name: "zero payment", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			(*pay)[0].Amount = decimal.Zero
		}, want: "invalid_payment_sign"},
		{name: "missing reversal target", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			(*pay)[0].Amount, (*pay)[0].ReversalOfID = decimal.NewFromInt(-25), 99
		}, want: "reversal_target_unavailable"},
		{name: "partial reversal", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			r := (*pay)[0]
			r.ID, r.ReversalOfID, r.Amount = 2, 1, decimal.NewFromInt(-24)
			*pay = append(*pay, r)
		}, want: "invalid_reversal"},
		{name: "duplicate reversal", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			r := (*pay)[0]
			r.ID, r.ReversalOfID, r.Amount = 2, 1, r.Amount.Neg()
			*pay = append(*pay, r)
			r.ID = 3
			*pay = append(*pay, r)
		}, want: "duplicate_reversal"},
		{name: "self reversal", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			(*pay)[0].Amount, (*pay)[0].ReversalOfID = decimal.NewFromInt(-25), 1
		}, want: "invalid_reversal"},
		{name: "planner rejects duplicate invoice identity", change: func(iv *[]readinessInvoice, _ *[]readinessPayment, _ *[]readinessLedger) {
			*iv = append(*iv, (*iv)[0])
		}, want: "planner_input_invalid"},
		{name: "valid exact reversal", change: func(_ *[]readinessInvoice, pay *[]readinessPayment, _ *[]readinessLedger) {
			r := (*pay)[0]
			r.ID, r.ReversalOfID, r.Amount = 2, 1, r.Amount.Neg()
			*pay = append(*pay, r)
		}},
		{name: "negative frozen gross is preserved", change: func(iv *[]readinessInvoice, _ *[]readinessPayment, _ *[]readinessLedger) {
			(*iv)[0].Gross = decimal.NewFromInt(-10)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invoices := []readinessInvoice{readinessTestInvoice()}
			payments := []readinessPayment{readinessTestPayment()}
			ledgers := []readinessLedger{{account: invoices[0].account, net: decimal.NewFromInt(10)}}
			if test.change != nil {
				test.change(&invoices, &payments, &ledgers)
			}
			beforeInvoices, beforePayments, beforeLedgers := slices.Clone(invoices), slices.Clone(payments), slices.Clone(ledgers)
			report := allocationReadiness(invoices, payments, ledgers)
			if !reflect.DeepEqual(beforeInvoices, invoices) || !reflect.DeepEqual(beforePayments, payments) || !reflect.DeepEqual(beforeLedgers, ledgers) {
				t.Fatal("readiness check mutated its inputs")
			}
			if report.CheckedAccounts != 1 {
				t.Fatalf("checked accounts=%d, want 1", report.CheckedAccounts)
			}
			if test.want == "" {
				if report.ReadyAccounts != 1 || len(report.Issues) != 0 {
					t.Fatalf("valid account was not ready: %+v", report)
				}
			} else if report.ReadyAccounts != 0 || !hasReadinessReason(report, test.want) {
				t.Fatalf("report=%+v, want unsupported reason %s", report, test.want)
			}
		})
	}
}

func TestAllocationReadinessCreditReferences(t *testing.T) {
	for _, test := range []struct {
		name, amount, reason string
		target               int64
		foreign              bool
	}{
		{name: "valid attached", amount: "-25", target: 1},
		{name: "valid standalone", amount: "-25"},
		{name: "positive credit", amount: "25", target: 1, reason: "invalid_credit_amount"},
		{name: "subcent credit", amount: "-0.005", target: 1, reason: "invalid_credit_amount"},
		{name: "missing or canceled target", amount: "-25", target: 99, reason: "credit_target_unavailable"},
		{name: "credit cannot reference a credit", amount: "-25", target: 2, reason: "credit_target_unavailable"},
		{name: "cross account", amount: "-25", target: 1, foreign: true, reason: "credit_target_account_mismatch"},
		{name: "excess attached credit", amount: "-101", target: 1, reason: "attached_credit_exceeds_gross"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invoice := readinessTestInvoice()
			credit := invoice
			credit.ID, credit.kind, credit.referenceID = 2, "gutschrift", test.target
			credit.Gross = decimal.RequireFromString(test.amount)
			if test.foreign {
				credit.account.neighborID = 3
			}
			report := allocationReadiness([]readinessInvoice{credit, invoice}, nil, nil)
			if test.reason == "" {
				if report.ReadyAccounts != 1 || len(report.Issues) != 0 {
					t.Fatalf("valid credit report=%+v", report)
				}
			} else if !hasReadinessReason(report, test.reason) || report.ReadyAccounts >= report.CheckedAccounts {
				t.Fatalf("report=%+v, want reason %s", report, test.reason)
			}
		})
	}
}

func TestAllocationReadinessReversalOwnershipAndAccountIsolation(t *testing.T) {
	payment := readinessTestPayment()
	reversal := payment
	reversal.ID, reversal.ReversalOfID, reversal.Amount = 2, 1, payment.Amount.Neg()
	reversal.account.neighborID = 3
	report := allocationReadiness(nil, []readinessPayment{payment, reversal}, nil)
	if report.CheckedAccounts != 2 || report.ReadyAccounts != 1 || !hasReadinessReason(report, "reversal_target_account_mismatch") {
		t.Fatalf("cross-account reversal was netted or poisoned the other account: %+v", report)
	}
	if report.Issues[0].NeighborID != 3 || report.Issues[0].RecordID != 2 {
		t.Fatalf("wrong reversal ownership in report: %+v", report.Issues)
	}
}

func TestAllocationReadinessEmptyAndLedgerOnlyAccounts(t *testing.T) {
	empty := allocationReadiness(nil, nil, nil)
	if empty.CheckedAccounts != 0 || empty.ReadyAccounts != 0 || empty.Issues == nil || len(empty.Issues) != 0 {
		t.Fatalf("empty report=%+v", empty)
	}
	report := allocationReadiness(nil, nil, []readinessLedger{{account: accountKey{1, 2}, net: decimal.NewFromInt(-3)}})
	if report.CheckedAccounts != 1 || report.ReadyAccounts != 1 || len(report.Issues) != 0 {
		t.Fatalf("ledger-only account report=%+v", report)
	}
}

func TestInvoiceAllocationReadinessReportContainsOnlyOpaqueMetadata(t *testing.T) {
	payment := readinessTestPayment()
	payment.Amount = decimal.RequireFromString("9876.5432")
	report := allocationReadiness(nil, []readinessPayment{payment}, nil)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Issues []map[string]any `json:"issues"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Issues) != 1 {
		t.Fatalf("unexpected issues: %s", raw)
	}
	keys := make([]string, 0, len(decoded.Issues[0]))
	for key := range decoded.Issues[0] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"entity", "neighbor_id", "reason", "record_id", "year_id"}) {
		t.Fatalf("report contains unexpected fields: %v", keys)
	}
}

func hasReadinessReason(report InvoiceAllocationReadiness, reason string) bool {
	return slices.ContainsFunc(report.Issues, func(issue InvoiceAllocationIssue) bool { return issue.Reason == reason })
}
