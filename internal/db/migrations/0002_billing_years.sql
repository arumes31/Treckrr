-- Separate the billing year (Abrechnungsjahr) from the pricing basis
-- (Bemessungsgrundlage). A basis is published roughly every few years and is
-- reused by several billing years; each billing year selects one basis and has
-- its own set of participating neighbours.

CREATE TABLE billing_years (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    year       INTEGER NOT NULL UNIQUE,
    base_id    BIGINT NOT NULL REFERENCES price_bases(id) ON DELETE RESTRICT,
    label      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Which neighbours participate in a given billing year.
CREATE TABLE billing_year_neighbors (
    billing_year_id BIGINT NOT NULL REFERENCES billing_years(id) ON DELETE CASCADE,
    neighbor_id     BIGINT NOT NULL REFERENCES neighbors(id) ON DELETE CASCADE,
    PRIMARY KEY (billing_year_id, neighbor_id)
);

-- Backfill: turn each existing basis (which was 1:1 with a year) into a
-- billing year pointing at that same basis.
INSERT INTO billing_years (year, base_id, label)
SELECT year, id, name FROM price_bases;

-- Point entries at the billing year instead of the basis.
ALTER TABLE entries ADD COLUMN billing_year_id BIGINT
    REFERENCES billing_years(id) ON DELETE CASCADE;
UPDATE entries e
   SET billing_year_id = by.id
  FROM billing_years by
 WHERE by.base_id = e.base_id;

-- Preserve existing behaviour: every current neighbour participates in every
-- existing billing year.
INSERT INTO billing_year_neighbors (billing_year_id, neighbor_id)
SELECT by.id, n.id FROM billing_years by CROSS JOIN neighbors n
ON CONFLICT DO NOTHING;

ALTER TABLE entries ALTER COLUMN billing_year_id SET NOT NULL;
ALTER TABLE entries DROP COLUMN base_id;

CREATE INDEX idx_entries_year ON entries(billing_year_id);
CREATE INDEX idx_entries_neighbor_year ON entries(neighbor_id, billing_year_id);

-- A basis is no longer tied to exactly one year; year on price_bases now just
-- documents when the basis was introduced ("gültig ab").
ALTER TABLE price_bases DROP CONSTRAINT IF EXISTS price_bases_year_key;
