-- Optional per-user preferences. No booking, payment or invoice data changes.
CREATE TABLE user_favorite_settings (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE TABLE user_favorites (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('neighbor', 'gespann', 'task')),
    value TEXT NOT NULL CHECK (length(value) BETWEEN 1 AND 100),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind, value)
);
