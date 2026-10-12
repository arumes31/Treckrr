package config

import (
	"strings"
	"testing"
)

func TestRehearsalScheduleOptIn(t *testing.T) {
	for _, tt := range []struct {
		name, cron, target, key string
		wantError               bool
	}{
		{"default manual", "", "", "", false},
		{"invalid cron", "invalid", "postgres://test/postgres", "test-encryption-key-long-enough", true},
		{"missing target", "0 5 1 * *", "", "test-encryption-key-long-enough", true},
		{"missing key", "0 5 1 * *", "postgres://test/postgres", "", true},
		{"explicit opt in", "0 5 1 * *", "postgres://test/postgres", "test-encryption-key-long-enough", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test:synthetic@localhost/treckrr")
			t.Setenv("SESSION_SECRET", strings.Repeat("a", 40))
			t.Setenv("ADMIN_PASSWORD", "test-admin-password-long-enough")
			t.Setenv("TRUST_PROXY", "false")
			t.Setenv("BACKUP_REHEARSE_CRON", tt.cron)
			t.Setenv("BACKUP_REHEARSE_URL", tt.target)
			t.Setenv("BACKUP_ENCRYPTION_KEY", tt.key)
			_, err := Load()
			if (err != nil) != tt.wantError {
				t.Fatalf("Load error=%v, wanted error=%v", err, tt.wantError)
			}
		})
	}
}
