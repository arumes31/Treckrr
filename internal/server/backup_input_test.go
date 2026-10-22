package server

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d0linger/treckrr/internal/backup"
	"github.com/d0linger/treckrr/internal/store"
)

// multipartKeyRequest builds a multipart POST carrying only the "key" field.
func multipartKeyRequest(t *testing.T, path, key string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("key", key); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// TestOversizedBackupKeyRejected proves the backup-key length guard (PR #104):
// an over-100-char key is rejected before any Argon2id key derivation, on both
// the JSON validate path and the form restore path.
func TestOversizedBackupKeyRejected(t *testing.T) {
	s := testServer()
	// A non-empty key makes the service Enabled(); the value is an obvious,
	// low-entropy test string (not a secret).
	s.backup = backup.New(backup.Options{EncKey: "not-a-real-secret"}, nil)
	oversized := strings.Repeat("A", maxBackupInputLen+1)

	t.Run("validate (JSON) rejects oversized key", func(t *testing.T) {
		req := multipartKeyRequest(t, "/admin/backup/validate", oversized)
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()
		s.handleBackupValidate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json: %v", err)
		}
		if resp["ok"] != false {
			t.Errorf("expected ok=false, got %v", resp["ok"])
		}
		if msg, _ := resp["message"].(string); !strings.Contains(msg, "höchstens 100 Zeichen") {
			t.Errorf("expected length warning, got %q", msg)
		}
	})

	t.Run("restore rejects oversized key with a redirect", func(t *testing.T) {
		req := multipartKeyRequest(t, "/admin/backup/restore", oversized)
		rr := httptest.NewRecorder()
		s.handleBackupRestore(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("expected 303 redirect, got %d", rr.Code)
		}
	})

	t.Run("a normal-length key passes the guard", func(t *testing.T) {
		// A short key clears the length check and proceeds (then fails later for a
		// missing file — a different error, proving the guard did not trip).
		req := multipartKeyRequest(t, "/admin/backup/validate", "short-key")
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()
		s.handleBackupValidate(rr, req)
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json: %v", err)
		}
		if msg, _ := resp["message"].(string); strings.Contains(msg, "höchstens 100 Zeichen") {
			t.Errorf("normal key must not trip the length guard, got %q", msg)
		}
	})
}

// TestRestoreRequiresInstanceKey proves the GUI restore refuses any key other
// than this instance's BACKUP_ENCRYPTION_KEY before it reads the upload, so an
// admin cannot feed pg_restore an archive encrypted under a key of their choice.
func TestRestoreRequiresInstanceKey(t *testing.T) {
	s := testServer()
	s.backup = backup.New(backup.Options{EncKey: "not-a-real-secret"}, nil)
	// The refusal is audited; an unreachable DB just makes that write fail and log.
	db, err := sql.Open("pgx", "postgres://u:p@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.store = store.New(db, "test-encryption-key-at-least-32-bytes!!")

	req := multipartKeyRequest(t, "/admin/backup/restore", "attacker-chosen-key")
	rr := httptest.NewRecorder()
	s.handleBackupRestore(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	// The flash payload is base64 before the HMAC; decode it to check which guard fired.
	var flash string
	for _, c := range rr.Result().Cookies() {
		if payload, _, ok := strings.Cut(c.Value, "."); ok {
			if b, err := base64.RawURLEncoding.DecodeString(payload); err == nil {
				flash += string(b)
			}
		}
	}
	if !strings.Contains(flash, "BACKUP_ENCRYPTION_KEY") {
		t.Errorf("expected the instance-key refusal, got flash %q", flash)
	}
}

func TestMatchesKey(t *testing.T) {
	bk := backup.New(backup.Options{EncKey: "not-a-real-secret"}, nil)
	if !bk.MatchesKey("not-a-real-secret") {
		t.Error("instance key must match")
	}
	for _, k := range []string{"", "not-a-real-secreT", "not-a-real-secret-x", "other"} {
		if bk.MatchesKey(k) {
			t.Errorf("key %q must not match", k)
		}
	}
	if backup.New(backup.Options{}, nil).MatchesKey("anything") {
		t.Error("a disabled service must match no key")
	}
}
