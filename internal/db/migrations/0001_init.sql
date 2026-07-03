-- Users and authentication -------------------------------------------------
CREATE TABLE users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    is_admin      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

-- Pricing basis (Bemessungsgrundlage) per year -----------------------------
CREATE TABLE price_bases (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    year       INTEGER NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    locked     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Load levels (Belastungsstufen): cost per PS per hour ---------------------
CREATE TABLE load_levels (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    base_id     BIGINT NOT NULL REFERENCES price_bases(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    cost_per_ps NUMERIC(12,4) NOT NULL,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (base_id, name)
);

-- Tractors: hourly rate = ps * load_level.cost_per_ps ----------------------
CREATE TABLE tractors (
    id      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    base_id BIGINT NOT NULL REFERENCES price_bases(id) ON DELETE CASCADE,
    ident   TEXT NOT NULL,
    name    TEXT NOT NULL DEFAULT '',
    ps      NUMERIC(10,2) NOT NULL,
    UNIQUE (base_id, ident)
);

-- Machines: hourly rate = working_width * cost_per_ab ----------------------
CREATE TABLE machines (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    base_id       BIGINT NOT NULL REFERENCES price_bases(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    working_width NUMERIC(10,3) NOT NULL,
    cost_per_ab   NUMERIC(12,4) NOT NULL,
    UNIQUE (base_id, name)
);

-- Fixed combinations (Gespanne) --------------------------------------------
CREATE TABLE gespanne (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    base_id       BIGINT NOT NULL REFERENCES price_bases(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    tractor_id    BIGINT REFERENCES tractors(id) ON DELETE SET NULL,
    load_level_id BIGINT REFERENCES load_levels(id) ON DELETE SET NULL,
    UNIQUE (base_id, name)
);

CREATE TABLE gespann_machines (
    gespann_id BIGINT NOT NULL REFERENCES gespanne(id) ON DELETE CASCADE,
    machine_id BIGINT NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    PRIMARY KEY (gespann_id, machine_id)
);

-- Neighbours (Nachbarn) are global; entries are tied to a year ------------
CREATE TABLE neighbors (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Booked work entries. Snapshots keep rows self-contained & export-safe. ---
CREATE TABLE entries (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    neighbor_id    BIGINT NOT NULL REFERENCES neighbors(id) ON DELETE CASCADE,
    base_id        BIGINT NOT NULL REFERENCES price_bases(id) ON DELETE CASCADE,
    entry_date     DATE NOT NULL,
    task_label     TEXT NOT NULL DEFAULT '',
    gespann_id     BIGINT REFERENCES gespanne(id) ON DELETE SET NULL,
    tractor_id     BIGINT REFERENCES tractors(id) ON DELETE SET NULL,
    load_level_id  BIGINT REFERENCES load_levels(id) ON DELETE SET NULL,
    tractor_label  TEXT NOT NULL,
    load_label     TEXT NOT NULL,
    machine_labels TEXT NOT NULL DEFAULT '',
    hours          NUMERIC(10,3) NOT NULL,
    hourly_rate    NUMERIC(12,4) NOT NULL,
    cost           NUMERIC(14,4) NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_entries_neighbor_base ON entries(neighbor_id, base_id);
CREATE INDEX idx_entries_base ON entries(base_id);

CREATE TABLE entry_machines (
    entry_id   BIGINT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    machine_id BIGINT REFERENCES machines(id) ON DELETE SET NULL,
    PRIMARY KEY (entry_id, machine_id)
);
