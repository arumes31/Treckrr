-- Soft deactivation so historical bookings stay intact and traceable.

-- Neighbours can be archived (hidden from new assignments) instead of deleted
-- once they have bookings.
ALTER TABLE neighbors ADD COLUMN archived BOOLEAN NOT NULL DEFAULT FALSE;

-- Tractors and machines can be deactivated when no longer used, without losing
-- the data behind existing bookings.
ALTER TABLE tractors ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE machines ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE;
