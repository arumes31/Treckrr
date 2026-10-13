//go:build integration

package store_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/d0linger/treckrr/internal/config"
	"github.com/d0linger/treckrr/internal/mail"
	"github.com/d0linger/treckrr/internal/store"
)

func TestDunningMailSettlementGuardIntegration(t *testing.T) {
	for _, scenario := range []string{"unchanged", "partial", "paid", "offset", "canceled", "unbooked fee", "legacy", "unbound legacy"} {
		t.Run(scenario, func(t *testing.T) {
			st, pool, yearID, neighborID, _ := invoiceFixture(t, false)
			invoice := issueFixtureInvoice(t, st, yearID, neighborID)
			open := invoice.Content.Gross
			message := store.OutboxMail{
				Kind: "mahnung", BillingYearID: yearID, NeighborID: neighborID, InvoiceID: invoice.ID,
				Recipient: "synthetic@example.invalid", Subject: "Synthetic reminder", Body: "Original demand 100",
				AttData: []byte("synthetic retained attachment"), DeliveryKey: "synthetic-reminder",
				Meta: store.OutboxMeta{InvoiceNumber: invoice.Number, OpenAmount: &open},
			}
			intent, _, err := st.CreateMailIntent(t.Context(), message)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AttemptMail(t.Context(), intent.ID, func(context.Context, string, string, string, string, string, string, []byte) error {
				return errors.New("synthetic SMTP unavailable")
			}); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "partial", "paid":
				amount := dec("40")
				if scenario == "paid" {
					amount = dec("100")
				}
				err = st.AddPayment(t.Context(), yearID, neighborID, amount, day(2026, 9, 11), "synthetic", "bank")
			case "offset":
				_, err = st.AddNeighborLedger(t.Context(), yearID, neighborID, dec("-10"), "synthetic offset", day(2026, 9, 11))
			case "canceled":
				_, err = st.StornoInvoiceByID(t.Context(), yearID, neighborID, invoice.ID, "synthetic cancellation")
			case "unbooked fee":
				_, err = pool.ExecContext(t.Context(), `UPDATE mail_outbox SET meta=meta || '{"fee":"5"}'::jsonb WHERE id=$1`, intent.ID)
			case "legacy":
				_, err = pool.ExecContext(t.Context(), `UPDATE mail_outbox SET meta=meta-'open_amount' WHERE id=$1`, intent.ID)
			case "unbound legacy":
				_, err = pool.ExecContext(t.Context(), `UPDATE mail_outbox SET invoice_id=NULL,meta=meta-'open_amount' WHERE id=$1`, intent.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.ExecContext(t.Context(), `UPDATE mail_outbox SET next_attempt_at=now() WHERE id=$1`, intent.ID); err != nil {
				t.Fatal(err)
			}
			calls := 0
			_, _, err = st.ProcessMailOutbox(t.Context(), func(context.Context, string, string, string, string, string, string, []byte) error {
				calls++
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantCalls := store.MailStatusHeld, 0
			if scenario == "unchanged" {
				wantStatus, wantCalls = store.MailStatusSent, 1
			}
			var status, body, identity string
			var attachment []byte
			if err := pool.QueryRowContext(t.Context(), `SELECT status,body,message_id,att_data FROM mail_outbox WHERE id=$1`, intent.ID).
				Scan(&status, &body, &identity, &attachment); err != nil {
				t.Fatal(err)
			}
			if status != wantStatus || calls != wantCalls || body != message.Body || string(attachment) != string(message.AttData) || identity != intent.MessageID {
				t.Fatalf("stale delivery or rewritten payload: status=%s calls=%d", status, calls)
			}
			var notices int
			if err := pool.QueryRowContext(t.Context(), `SELECT count(*) FROM dunning_notices`).Scan(&notices); err != nil || notices != wantCalls {
				t.Fatalf("notice count=%d want=%d err=%v", notices, wantCalls, err)
			}
			if scenario == "partial" || scenario == "unbound legacy" {
				updated := dec("60")
				if scenario == "unbound legacy" {
					updated = dec("100")
				}
				message.Meta.OpenAmount, message.Body, message.RetryFailed = &updated, "Fresh demand 60", true
				fresh, created, err := st.CreateMailIntent(t.Context(), message)
				if err != nil || !created || (scenario == "partial" && fresh.ID != intent.ID) ||
					(scenario == "unbound legacy" && fresh.ID == intent.ID) {
					t.Fatalf("fresh reminder was not regenerated: %v", err)
				}
				status, err = st.AttemptMail(t.Context(), fresh.ID, func(_ context.Context, _, _, _, body, _, _ string, _ []byte) error {
					if body != "Fresh demand 60" {
						t.Fatal("obsolete body sent")
					}
					return nil
				})
				if err != nil || status != store.MailStatusSent {
					t.Fatalf("fresh delivery=%s %v", status, err)
				}
				if scenario == "unbound legacy" {
					repeated, created, err := st.CreateMailIntent(t.Context(), message)
					if err != nil || created || repeated.ID != fresh.ID {
						t.Fatalf("legacy regeneration was not idempotent: %v", err)
					}
				}
			}
		})
	}
}

func TestDunningMailPaymentBeforeSMTPDataIntegration(t *testing.T) {
	st, pool, yearID, neighborID, _ := invoiceFixture(t, false)
	invoice := issueFixtureInvoice(t, st, yearID, neighborID)
	open := invoice.Content.Gross
	intent, _, err := st.CreateMailIntent(t.Context(), store.OutboxMail{
		Kind: "mahnung", BillingYearID: yearID, NeighborID: neighborID, InvoiceID: invoice.ID,
		Recipient: "synthetic@example.invalid", Subject: "Synthetic reminder", Body: "Original demand",
		Meta: store.OutboxMeta{InvoiceNumber: invoice.Number, OpenAmount: &open},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var sawData atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = connection.Write([]byte("220 synthetic ESMTP\r\n"))
		reader := bufio.NewReader(connection)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "DATA") {
				sawData.Store(true)
				_, _ = connection.Write([]byte("550 unexpected DATA\r\n"))
				return
			}
			_, _ = connection.Write([]byte("250 synthetic\r\n"))
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SMTPHost: host, SMTPPort: port, SMTPFrom: "synthetic@example.invalid"}
	status, err := st.AttemptMail(t.Context(), intent.ID,
		func(ctx context.Context, id, to, subject, body, _, _ string, _ []byte) error {
			// The outbox has already been claimed. The DATA hook must observe
			// this payment even though admission saw an open invoice.
			if err := st.AddPayment(t.Context(), yearID, neighborID, open, day(2026, 9, 11), "synthetic", "bank"); err != nil {
				return err
			}
			return mail.SendWithMessageID(ctx, cfg, to, subject, body, nil, id)
		})
	if err != nil || status != store.MailStatusHeld || sawData.Load() {
		t.Fatalf("stale DATA: status=%s data=%v err=%v", status, sawData.Load(), err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("synthetic SMTP did not stop")
	}
	var attempts, history int
	if err := pool.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM mail_outbox_attempts WHERE outbox_id=$1 AND outcome='failed' AND data_started_at IS NULL AND finished_at IS NOT NULL),
		(SELECT count(*) FROM dunning_notices)+(SELECT count(*) FROM beleg_sends)`, intent.ID).Scan(&attempts, &history); err != nil || attempts != 1 || history != 0 {
		t.Fatalf("incorrect pre-DATA bookkeeping: %d/%d, %v", attempts, history, err)
	}
}
