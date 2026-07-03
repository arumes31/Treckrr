package server

import (
	"net/http"

	"treckrr/internal/totp"
)

// ---- Forced / voluntary password change ---------------------------------

func (s *Server) handleAccountPasswordForm(w http.ResponseWriter, r *http.Request) {
	user := userFromCtx(r)
	data := s.newPage(w, r, "Passwort ändern", "profile")
	data["Forced"] = user.MustChangePassword
	s.render(w, r, "account_password", data)
}

func (s *Server) handleAccountPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungültige Anfrage", http.StatusBadRequest)
		return
	}
	user := userFromCtx(r)
	current := r.FormValue("current_password")
	next := r.FormValue("new_password")

	if _, err := s.store.AuthenticateUser(r.Context(), user.Username, current); err != nil {
		s.setFlash(w, "error", "Aktuelles Passwort ist falsch.")
		redirect(w, r, "/account/password")
		return
	}
	if msg := passwordPolicyError(next); msg != "" {
		s.setFlash(w, "error", msg)
		redirect(w, r, "/account/password")
		return
	}
	if err := s.store.UpdatePassword(r.Context(), user.ID, next); err != nil {
		http.Error(w, "Interner Fehler", http.StatusInternalServerError)
		return
	}
	_ = s.store.SetMustChangePassword(r.Context(), user.ID, false)
	s.audit(r, "password_change", "user", user.ID, "eigenes Passwort")
	s.setFlash(w, "success", "Passwort geändert.")
	redirect(w, r, "/profile")
}

// ---- Two-factor authentication (TOTP) -----------------------------------

// handleTwoFactor shows the 2FA setup / status page. When 2FA is not yet
// enabled it generates (and persists as pending) a secret to display.
func (s *Server) handleTwoFactor(w http.ResponseWriter, r *http.Request) {
	user := userFromCtx(r)
	data := s.newPage(w, r, "Zwei‑Faktor", "profile")
	data["Enabled"] = user.TotpEnabled

	if !user.TotpEnabled {
		secret, err := s.store.GetTotpSecret(r.Context(), user.ID)
		if err != nil || secret == "" {
			secret, err = totp.GenerateSecret()
			if err != nil {
				http.Error(w, "Interner Fehler", http.StatusInternalServerError)
				return
			}
			if err := s.store.SetTotp(r.Context(), user.ID, false, secret); err != nil {
				http.Error(w, "Interner Fehler", http.StatusInternalServerError)
				return
			}
		}
		data["Secret"] = secret
		data["URI"] = totp.ProvisioningURI(secret, user.Username, "Treckrr")
	}
	s.render(w, r, "account_2fa", data)
}

func (s *Server) handleTwoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungültige Anfrage", http.StatusBadRequest)
		return
	}
	user := userFromCtx(r)
	secret, err := s.store.GetTotpSecret(r.Context(), user.ID)
	if err != nil || secret == "" {
		s.setFlash(w, "error", "Kein ausstehendes 2FA‑Geheimnis. Bitte erneut starten.")
		redirect(w, r, "/account/2fa")
		return
	}
	if !totp.Validate(secret, r.FormValue("code")) {
		s.setFlash(w, "error", "Code ungültig. Bitte erneut versuchen.")
		redirect(w, r, "/account/2fa")
		return
	}
	if err := s.store.SetTotp(r.Context(), user.ID, true, secret); err != nil {
		http.Error(w, "Interner Fehler", http.StatusInternalServerError)
		return
	}
	s.audit(r, "2fa_enable", "user", user.ID, "")
	s.setFlash(w, "success", "Zwei‑Faktor‑Authentifizierung aktiviert.")
	redirect(w, r, "/profile")
}

func (s *Server) handleTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungültige Anfrage", http.StatusBadRequest)
		return
	}
	user := userFromCtx(r)
	// Require the current password to disable 2FA.
	if _, err := s.store.AuthenticateUser(r.Context(), user.Username, r.FormValue("password")); err != nil {
		s.setFlash(w, "error", "Passwort falsch – 2FA nicht deaktiviert.")
		redirect(w, r, "/account/2fa")
		return
	}
	if err := s.store.SetTotp(r.Context(), user.ID, false, ""); err != nil {
		http.Error(w, "Interner Fehler", http.StatusInternalServerError)
		return
	}
	s.audit(r, "2fa_disable", "user", user.ID, "")
	s.setFlash(w, "success", "Zwei‑Faktor‑Authentifizierung deaktiviert.")
	redirect(w, r, "/profile")
}

// ---- Session management --------------------------------------------------

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungültige Anfrage", http.StatusBadRequest)
		return
	}
	user := userFromCtx(r)
	if err := s.store.DeleteSessionForUser(r.Context(), user.ID, r.FormValue("token")); err != nil {
		s.setFlash(w, "error", "Sitzung konnte nicht beendet werden.")
	} else {
		s.audit(r, "session_revoke", "user", user.ID, "")
		s.setFlash(w, "success", "Sitzung beendet.")
	}
	redirect(w, r, "/profile")
}

func (s *Server) handleSessionRevokeOthers(w http.ResponseWriter, r *http.Request) {
	user := userFromCtx(r)
	current := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		current = c.Value
	}
	if err := s.store.DeleteUserSessionsExcept(r.Context(), user.ID, current); err != nil {
		s.setFlash(w, "error", "Aktion fehlgeschlagen.")
	} else {
		s.audit(r, "session_revoke_others", "user", user.ID, "")
		s.setFlash(w, "success", "Alle anderen Sitzungen wurden beendet.")
	}
	redirect(w, r, "/profile")
}
