//go:build integration

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/d0linger/treckrr/internal/backup"
	"github.com/d0linger/treckrr/internal/config"
	"github.com/d0linger/treckrr/internal/store"
)

func TestBackupRehearsalNotificationAfterDismissedWarningIntegration(t *testing.T) {
	e := newItEnv(t)
	var userID int64
	if err := e.pool.QueryRowContext(e.ctx, `SELECT id FROM users WHERE username=$1`, e.uname).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	srv := &Server{
		cfg:   &config.Config{BackupStatusFile: filepath.Join(dir, "status.json")},
		store: e.st,
		backup: backup.New(backup.Options{
			EncKey: "test-only-backup-key", SkipStartupCleanup: true,
		}, nil),
	}
	status := backup.Status{LastBackup: time.Now().UTC().Add(-72 * time.Hour), OK: true, Encrypted: true}
	refresh := func() []store.Notification {
		t.Helper()
		data, err := json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.WriteFile("status.json", data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := srv.RefreshNotifications(e.ctx); err != nil {
			t.Fatal(err)
		}
		all, err := e.st.ListNotifications(e.ctx, userID, 200)
		if err != nil {
			t.Fatal(err)
		}
		var notices []store.Notification
		for _, notice := range all {
			if notice.Kind == "backup" {
				notices = append(notices, notice)
			}
		}
		return notices
	}
	stale := refresh()
	if len(stale) != 1 || stale[0].Title != "Backup veraltet" {
		t.Fatalf("stale backup notice: %+v", stale)
	}
	if err := e.st.DismissNotification(e.ctx, userID, stale[0].ID); err != nil {
		t.Fatal(err)
	}
	failed := false
	status.RehearsalOK = &failed
	notices := refresh()
	if len(notices) != 1 || notices[0].ID == stale[0].ID || notices[0].Title != "Restore-Probe fehlgeschlagen" ||
		notices[0].Tone != "bad" || notices[0].ReadAt != nil || notices[0].Href != "/admin/backup" {
		t.Fatalf("restore failure hidden by dismissed backup warning: %+v", notices)
	}
	firstFailure := notices[0].ID
	if again := refresh(); len(again) != 1 || again[0].ID != firstFailure {
		t.Fatalf("retry duplicated restore failure: %+v", again)
	}
	if err := e.st.DismissNotification(e.ctx, userID, firstFailure); err != nil {
		t.Fatal(err)
	}
	// Ordinary backup progress must not revive the same dismissed failure.
	status.LastBackup = time.Now().UTC()
	if again := refresh(); len(again) != 0 {
		t.Fatalf("backup progress duplicated restore failure: %+v", again)
	}
	succeeded := true
	status.RehearsalOK = &succeeded
	status.RestoreTested = time.Now().UTC()
	if healthy := refresh(); len(healthy) != 0 {
		t.Fatalf("healthy backup generated warning: %+v", healthy)
	}
	status.RehearsalOK = &failed
	if next := refresh(); len(next) != 1 || next[0].ID == firstFailure || next[0].ReadAt != nil {
		t.Fatalf("new failure episode did not generate a fresh notification: %+v", next)
	}
}
