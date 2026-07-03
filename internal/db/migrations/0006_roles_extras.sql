-- User roles & security -----------------------------------------------------
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'editor';
UPDATE users SET role = 'admin' WHERE is_admin;
ALTER TABLE users ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Session metadata (for the "active sessions" management view) --------------
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN last_seen TIMESTAMPTZ NOT NULL DEFAULT now();

-- Voidable bookings (cancel instead of delete) -----------------------------
ALTER TABLE entries ADD COLUMN voided BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE entries ADD COLUMN void_reason TEXT NOT NULL DEFAULT '';

-- Ordering & categorisation of master data ---------------------------------
ALTER TABLE machines ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE machines ADD COLUMN category TEXT NOT NULL DEFAULT '';
ALTER TABLE tractors ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE gespanne ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
