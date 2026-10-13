-- Preserve every existing snapshot, hash and financial value. NULL means the
-- original payment term was not persisted; do not infer historical terms.
ALTER TABLE invoices ADD COLUMN payment_term_days INTEGER
    CHECK (payment_term_days BETWEEN 0 AND 365);
