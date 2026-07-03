-- Per-year workflow status: 'in_progress' while booking, 'completed' when the
-- year is closed for billing.
ALTER TABLE billing_years ADD COLUMN status TEXT NOT NULL DEFAULT 'in_progress';

-- Payment tracking per neighbour per year (relevant once the year is completed).
ALTER TABLE billing_year_neighbors ADD COLUMN paid BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE billing_year_neighbors ADD COLUMN paid_at TIMESTAMPTZ;
