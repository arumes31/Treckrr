-- Keep existing snapshots and amounts byte-for-byte unchanged. Only provenance
-- and document-selection metadata is added; ambiguous history aborts startup.
-- Snapshot hashes remain opaque: source matching never rebuilds an old document.
-- Multi-invoice provenance and explicit payment/document selection.
-- The migration runner applies this entire file in one transaction. A legacy
-- ambiguity aborts all DDL and backfills; financial rows are never rounded,
-- rebuilt, repriced or reassigned. Only new metadata is populated.
LOCK TABLE invoices, entries, payments, neighbor_ledger, beleg_shares,
    beleg_sends, mail_outbox IN SHARE ROW EXCLUSIVE MODE;

DO $$
DECLARE bad_id BIGINT;
BEGIN
    SELECT id INTO bad_id FROM invoices
     WHERE kind IN ('invoice','gutschrift') AND status='issued'
       AND (issued_on='0001-01-01'::date OR NOT isfinite(issued_on))
     ORDER BY id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: invalid issued date for invoice %', bad_id;
    END IF;
    SELECT id INTO bad_id FROM payments
     WHERE paid_on='0001-01-01'::date OR NOT isfinite(paid_on)
     ORDER BY id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: invalid payment date for payment %', bad_id;
    END IF;
    SELECT id INTO bad_id FROM invoices
     WHERE kind='invoice' AND status='issued'
       AND (net IS NULL OR gross IS NULL OR lines IS NULL
            OR jsonb_typeof(lines)<>'array' OR content_hash='')
     ORDER BY id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: incomplete snapshot for invoice %', bad_id;
    END IF;
    SELECT id INTO bad_id FROM invoices
     WHERE kind='invoice' AND status='issued' AND jsonb_array_length(lines)=0
     ORDER BY id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: empty snapshot for invoice %', bad_id;
    END IF;
    -- Match the Go snapshot decoder's types instead of coercing JSON text.
    -- The date prefix alone is not sufficient: a malformed trailing timestamp
    -- must not make an apparently matching source eligible for assignment.
    SELECT i.id INTO bad_id FROM invoices i
      CROSS JOIN LATERAL jsonb_array_elements(i.lines) AS l(value)
     WHERE i.kind='invoice' AND i.status='issued'
       AND (jsonb_typeof(l.value)<>'object'
         OR jsonb_typeof(l.value->'date') IS DISTINCT FROM 'string'
         OR jsonb_typeof(l.value->'label') IS DISTINCT FROM 'string'
         OR jsonb_typeof(l.value->'unit') IS DISTINCT FROM 'string'
         OR COALESCE(jsonb_typeof(l.value->'qty'),'null') NOT IN ('number','string')
         OR COALESCE(jsonb_typeof(l.value->'unit_price'),'null') NOT IN ('number','string')
         OR COALESCE(jsonb_typeof(l.value->'cost'),'null') NOT IN ('number','string')
         OR (l.value->>'date') !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T(0[0-9]|1[0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]([.][0-9]{1,9})?(Z|[+-](0[0-9]|1[0-9]|2[0-3]):[0-5][0-9])$'
         OR (l.value->>'date')='0001-01-01T00:00:00Z')
     ORDER BY i.id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: invalid snapshot types for invoice %', bad_id;
    END IF;
    -- Force complete timestamp parsing as well as checking its shape. This
    -- catches invalid calendar dates and clock/zone fields transactionally.
    PERFORM (l.value->>'date')::timestamptz FROM invoices i
      CROSS JOIN LATERAL jsonb_array_elements(i.lines) AS l(value)
     WHERE i.kind='invoice' AND i.status='issued';

    IF EXISTS(
        SELECT 1 FROM invoices i
        CROSS JOIN LATERAL jsonb_array_elements(i.lines) AS l(value)
        WHERE i.kind='invoice' AND i.status='issued'
          AND (l.value->>'date')::timestamptz='0001-01-01 00:00:00+00'::timestamptz
    ) THEN
        RAISE EXCEPTION 'multi-invoice migration: zero snapshot date';
    END IF;

    -- Match the issuer's exact ordered projection, including legacy empty hour
    -- units and four-decimal booking costs. Mutable party/master data and tax
    -- settings are deliberately not used to reconstruct historical documents.
    WITH sources AS (
        SELECT i.id AS invoice_id, e.id AS entry_id,
               row_number() OVER (PARTITION BY i.id ORDER BY e.entry_date,e.id) AS ordinal,
               e.entry_date, COALESCE(NULLIF(e.task_label,''),NULLIF(e.note,''),'Sonstige') AS label,
               e.unit,
               CASE WHEN e.unit<>'' AND e.unit<>'h' THEN e.quantity ELSE e.hours END AS qty,
               CASE WHEN e.unit<>'' AND e.unit<>'h' THEN e.unit_price ELSE e.hourly_rate END AS unit_price,
               e.cost
          FROM invoices i JOIN entries e
            ON e.billing_year_id=i.billing_year_id AND e.neighbor_id=i.neighbor_id AND NOT e.voided
         WHERE i.kind='invoice' AND i.status='issued'
    ), lines AS (
        SELECT i.id AS invoice_id,l.ordinal,l.value
          FROM invoices i CROSS JOIN LATERAL jsonb_array_elements(i.lines) WITH ORDINALITY AS l(value,ordinal)
         WHERE i.kind='invoice' AND i.status='issued'
    ), mismatches AS (
        SELECT COALESCE(s.invoice_id,l.invoice_id) AS invoice_id
          FROM sources s FULL JOIN lines l USING(invoice_id,ordinal)
         WHERE s.entry_id IS NULL OR l.ordinal IS NULL
            OR s.entry_date IS DISTINCT FROM (left(l.value->>'date',10))::date
            OR s.label IS DISTINCT FROM l.value->>'label'
            OR s.unit IS DISTINCT FROM l.value->>'unit'
            OR s.qty IS DISTINCT FROM (l.value->>'qty')::numeric
            OR s.unit_price IS DISTINCT FROM (l.value->>'unit_price')::numeric
            OR s.cost IS DISTINCT FROM (l.value->>'cost')::numeric
        UNION ALL
        SELECT i.id FROM invoices i WHERE i.kind='invoice' AND i.status='issued'
           AND i.net IS DISTINCT FROM (SELECT ROUND(COALESCE(SUM(e.cost),0),2) FROM entries e
                WHERE e.billing_year_id=i.billing_year_id AND e.neighbor_id=i.neighbor_id AND NOT e.voided)
    ) SELECT invoice_id INTO bad_id FROM mismatches ORDER BY invoice_id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: ambiguous source assignment for invoice %', bad_id;
    END IF;

    -- Old sub-cent cash is legal historical data (0062). Do not silently round
    -- it to make the new allocator accept it: that can change account balances.
    SELECT id INTO bad_id FROM payments
     WHERE amount<>round(amount,2) OR amount=0
            OR (reversal_of_payment_id IS NULL AND amount<0)
            OR (reversal_of_payment_id IS NOT NULL AND amount>=0)
     ORDER BY id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: unsupported historical payment %; values unchanged', bad_id;
    END IF;
    -- Check restorable/voidable rows individually too: two opposite sub-cent
    -- postings can net to whole cents now, then cease to do so after one is voided.
    SELECT id INTO bad_id FROM neighbor_ledger WHERE amount<>round(amount,2)
     ORDER BY id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: sub-cent ledger account at posting %; values unchanged', bad_id;
    END IF;
    SELECT p.id INTO bad_id FROM payments p LEFT JOIN payments original ON original.id=p.reversal_of_payment_id
     WHERE p.deleted_at IS NULL AND p.reversal_of_payment_id IS NOT NULL
       AND (original.id IS NULL OR original.deleted_at IS NOT NULL
            OR original.reversal_of_payment_id IS NOT NULL OR original.amount<=0
            OR original.amount<>-p.amount
            OR original.billing_year_id<>p.billing_year_id OR original.neighbor_id<>p.neighbor_id)
     ORDER BY p.id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: ambiguous payment reversal %', bad_id;
    END IF;
    SELECT c.id INTO bad_id FROM invoices c LEFT JOIN invoices original ON original.id=c.references_invoice_id
     WHERE c.kind='gutschrift' AND c.status='issued'
       AND (c.gross IS NULL OR c.gross>0 OR (c.references_invoice_id IS NOT NULL AND
            (original.id IS NULL OR original.kind<>'invoice' OR original.status<>'issued'
             OR original.billing_year_id<>c.billing_year_id OR original.neighbor_id<>c.neighbor_id)))
     ORDER BY c.id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: ambiguous credit note %', bad_id;
    END IF;
    SELECT i.id INTO bad_id FROM invoices i WHERE i.kind='invoice' AND i.status='issued'
       AND (SELECT COALESCE(-sum(c.gross),0) FROM invoices c WHERE c.kind='gutschrift'
             AND c.status='issued' AND c.references_invoice_id=i.id)>GREATEST(i.gross,0)
     ORDER BY i.id LIMIT 1;
    IF bad_id IS NOT NULL THEN
        RAISE EXCEPTION 'multi-invoice migration: excess attached credit for invoice %', bad_id;
    END IF;
END $$;

CREATE TABLE invoice_entries (
    invoice_id BIGINT NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,
    entry_id BIGINT REFERENCES entries(id) ON DELETE SET NULL,
    source_entry_id BIGINT NOT NULL,
    ordinal INTEGER NOT NULL CHECK(ordinal>0),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY(invoice_id,source_entry_id),
    UNIQUE(invoice_id,ordinal),
    CHECK(NOT active OR entry_id IS NOT NULL),
    CHECK(entry_id IS NULL OR entry_id=source_entry_id)
);
CREATE UNIQUE INDEX invoice_entries_active_source ON invoice_entries(entry_id) WHERE active;
INSERT INTO invoice_entries(invoice_id,entry_id,source_entry_id,ordinal)
SELECT i.id,e.id,e.id,row_number() OVER(PARTITION BY i.id ORDER BY e.entry_date,e.id)::integer
  FROM invoices i JOIN entries e ON e.billing_year_id=i.billing_year_id AND e.neighbor_id=i.neighbor_id AND NOT e.voided
 WHERE i.kind='invoice' AND i.status='issued';

ALTER TABLE invoices ADD COLUMN issue_request_hash TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX invoices_issue_request ON invoices(billing_year_id,neighbor_id,issue_request_hash)
    WHERE kind='invoice' AND status='issued' AND issue_request_hash<>'';
ALTER TABLE payments ADD COLUMN preferred_invoice_id BIGINT REFERENCES invoices(id) ON DELETE RESTRICT;
-- Legacy invoice_id is historical attribution, not an explicit allocation choice.
-- Preserve it unchanged, including references to canceled documents.

ALTER TABLE beleg_shares ADD COLUMN invoice_id BIGINT REFERENCES invoices(id) ON DELETE RESTRICT;
ALTER TABLE beleg_sends ADD COLUMN invoice_id BIGINT REFERENCES invoices(id) ON DELETE RESTRICT;
ALTER TABLE mail_outbox ADD COLUMN invoice_id BIGINT REFERENCES invoices(id) ON DELETE RESTRICT;
-- Bind legacy capabilities only if their creation falls after exactly one
-- historical invoice. Never make an old token silently grant a replacement.
UPDATE beleg_shares s SET invoice_id=i.id FROM invoices i
 WHERE i.kind='invoice' AND i.billing_year_id=s.billing_year_id AND i.neighbor_id=s.neighbor_id
   AND i.created_at<=s.created_at
   AND 1=(SELECT count(*) FROM invoices other WHERE other.kind='invoice'
       AND other.billing_year_id=s.billing_year_id AND other.neighbor_id=s.neighbor_id
       AND other.created_at<=s.created_at);
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM beleg_shares WHERE expires_at>now() AND invoice_id IS NULL) THEN
        RAISE EXCEPTION 'multi-invoice migration: ambiguous unexpired legacy share; no capabilities changed';
    END IF;
END $$;
-- Annual delivery history remains annual when identity is not provable. New
-- sends are document-scoped; no historical send/payment row is rewritten.
CREATE INDEX beleg_sends_invoice ON beleg_sends(invoice_id,sent_at DESC) WHERE invoice_id IS NOT NULL;
CREATE INDEX mail_outbox_invoice ON mail_outbox(invoice_id) WHERE invoice_id IS NOT NULL;

DROP INDEX invoices_active_per_neighbor_year;
CREATE INDEX invoices_active_account ON invoices(billing_year_id,neighbor_id,issued_on,id)
    WHERE kind='invoice' AND status='issued';
