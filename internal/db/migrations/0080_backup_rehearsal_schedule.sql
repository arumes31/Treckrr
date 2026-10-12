-- Optional restore rehearsals share the existing cluster scheduler lease.
-- Nullable clocks preserve disabled-by-default behavior on existing installs.
ALTER TABLE backup_scheduler_state
    ADD COLUMN rehearsal_last_success TIMESTAMPTZ,
    ADD COLUMN rehearsal_attempt_at TIMESTAMPTZ,
    ADD COLUMN rehearsal_retry_at TIMESTAMPTZ;
