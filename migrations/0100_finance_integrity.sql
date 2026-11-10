-- ADR-008 integrity: balanced journal, immutable issued invoices.

-- Credit notes and write-offs reduce what is owed without touching the issued document.
ALTER TABLE billing.invoices
  ADD COLUMN credited_minor bigint NOT NULL DEFAULT 0 CHECK (credited_minor >= 0),
  ADD COLUMN written_off_minor bigint NOT NULL DEFAULT 0 CHECK (written_off_minor >= 0);

CREATE INDEX invoices_ws_status ON billing.invoices(workspace_id, status);
CREATE INDEX journal_lines_entry ON billing.journal_lines(entry_id);
CREATE INDEX payments_invoice ON billing.payments(invoice_id);

-- Every journal entry has lines and debits == credits, checked at commit so lines can be inserted one by one.
CREATE OR REPLACE FUNCTION billing.assert_entry_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE eid uuid; d bigint; c bigint; n bigint;
BEGIN
  IF TG_TABLE_NAME = 'journal_entries' THEN eid := NEW.id; ELSE eid := NEW.entry_id; END IF;
  SELECT coalesce(sum(debit_minor), 0), coalesce(sum(credit_minor), 0), count(*) INTO d, c, n
  FROM billing.journal_lines WHERE entry_id = eid;
  IF n = 0 OR d <> c THEN
    RAISE EXCEPTION 'journal entry % does not balance (debits %, credits %, % lines)', eid, d, c, n;
  END IF;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER journal_entry_balanced AFTER INSERT ON billing.journal_entries
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION billing.assert_entry_balanced();
CREATE CONSTRAINT TRIGGER journal_line_balanced AFTER INSERT ON billing.journal_lines
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION billing.assert_entry_balanced();

-- Once an invoice leaves draft only settlement/IRN/cancel fields may change; corrections are credit notes.
CREATE OR REPLACE FUNCTION billing.guard_invoice() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE mutable text[] := ARRAY['status','paid_minor','credited_minor','written_off_minor','irn','ack_no',
                                'ack_date','signed_qr','irn_cancelled_at','cancelled_at'];
BEGIN
  IF OLD.status = 'draft' THEN
    RETURN CASE TG_OP WHEN 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'issued invoice % cannot be deleted; issue a credit note', OLD.number;
  END IF;
  IF NEW.status = 'draft' OR (to_jsonb(NEW) - mutable) IS DISTINCT FROM (to_jsonb(OLD) - mutable) THEN
    RAISE EXCEPTION 'issued invoice % is immutable; issue a credit note', OLD.number;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER invoices_immutable BEFORE UPDATE OR DELETE ON billing.invoices
  FOR EACH ROW EXECUTE FUNCTION billing.guard_invoice();

CREATE OR REPLACE FUNCTION billing.guard_invoice_line() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE st text;
BEGIN
  SELECT status INTO st FROM billing.invoices WHERE note_id = coalesce(NEW.invoice_id, OLD.invoice_id);
  IF st IS NOT NULL AND st <> 'draft' THEN
    RAISE EXCEPTION 'lines of an issued invoice are immutable; issue a credit note';
  END IF;
  RETURN CASE TG_OP WHEN 'DELETE' THEN OLD ELSE NEW END;
END $$;
CREATE TRIGGER invoice_lines_immutable BEFORE INSERT OR UPDATE OR DELETE ON billing.invoice_lines
  FOR EACH ROW EXECUTE FUNCTION billing.guard_invoice_line();
