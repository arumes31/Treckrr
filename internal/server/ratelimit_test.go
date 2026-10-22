package server

import (
	"context"
	"database/sql"
	"testing"

	"github.com/d0linger/treckrr/internal/store"
)

// TestAccountKey pins the normalization of the account-scoped login-limiter key:
// case-folded and trimmed, so failures against "Admin", "admin" and " admin "
// share one throttle bucket.
func TestAccountKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"admin", "pwuser:admin"},
		{"Admin", "pwuser:admin"},
		{"  Bob ", "pwuser:bob"},
		{"MiXeD", "pwuser:mixed"},
		{"", "pwuser:"},
	}
	for _, c := range cases {
		if got := accountKey(c.in); got != c.want {
			t.Errorf("accountKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShareBlockedFailsClosed pins that the public share limiter refuses when
// its rate-limit query cannot run, instead of silently disabling the throttle.
func TestShareBlockedFailsClosed(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://u:p@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	lim := newLoginLimiter(store.New(db, "test-encryption-key-at-least-32-bytes!!"))
	if !lim.shareBlocked(context.Background(), "192.0.2.1") {
		t.Fatal("shareBlocked must fail closed when the limiter store is unavailable")
	}
}
