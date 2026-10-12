-- Synthetic fixture shared by local Docker and CI. Execute only against the
-- disposable treckrr_e2e database after the app has applied its migrations.
DO $$ BEGIN
  IF current_database() <> 'treckrr_e2e' THEN
    RAISE EXCEPTION 'refusing to seed a non-E2E database';
  END IF;
  IF EXISTS (SELECT 1 FROM price_bases) THEN
    RAISE EXCEPTION 'fixture database is not empty';
  END IF;
END $$;
BEGIN;
UPDATE users SET must_change_password = false WHERE username = 'admin';
INSERT INTO price_bases (year, name) VALUES (2025, 'E2E-Basis');
INSERT INTO billing_years (year, base_id, label, status) VALUES (2025, 1, '2025', 'open');
INSERT INTO neighbors (name, address) VALUES ('E2E Nachbar', 'Feldweg 1, 4710 Testdorf');
INSERT INTO billing_year_neighbors (billing_year_id, neighbor_id) VALUES (1, 1);
INSERT INTO load_levels (base_id, name, cost_per_ps) VALUES (1, 'mittel', 0.36);
INSERT INTO tractors (base_id, ident, name, ps) VALUES (1, 'T1', 'E2E-Traktor', 100);
INSERT INTO machines (base_id, name, working_width, cost_per_ab) VALUES (1, 'E2E-Maschine', 2, 5);
INSERT INTO persons (name, hourly_rate) VALUES ('E2E Helfer', 18);
INSERT INTO entries (neighbor_id, billing_year_id, entry_date, task_label, hours, hourly_rate, cost,
                     unit, quantity, unit_price, tractor_label, load_label, machine_labels, note, void_reason)
  VALUES (1, 1, '2025-05-01', 'Mähen', 2, 46, 92, 'h', 2, 46, 'E2E-Traktor', 'mittel', 'E2E-Maschine', '', '');
INSERT INTO payments (billing_year_id, neighbor_id, amount, paid_on, note)
  VALUES (1, 1, 20, '2025-05-02', 'Anzahlung');
COMMIT;
