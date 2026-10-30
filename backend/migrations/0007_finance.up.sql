-- Keuangan mitra: rekening pencairan terenkripsi, pencairan (payout), refund, ledger tak bisa diubah.
-- Lihat docs/04-pembayaran-dan-ledger.md.

-- Rekening pencairan = rekening dari verifikasi yang disetujui (disalin saat super admin menyetujui).
-- Tabel belum pernah berisi data, jadi kolom teks biasa langsung diganti kolom terenkripsi.
ALTER TABLE payout_accounts
    DROP COLUMN account_number,
    DROP COLUMN account_name,
    ADD COLUMN bank_name       text NOT NULL DEFAULT '',
    ADD COLUMN account_holder  text NOT NULL,
    ADD COLUMN account_enc     text NOT NULL,  -- ciphertext secretbox ("v1:…"), sama seperti org_verifications
    ADD COLUMN account_last4   text NOT NULL,
    ADD COLUMN verification_id uuid REFERENCES org_verifications(id) ON DELETE SET NULL,
    ADD CONSTRAINT payout_accounts_org_uniq UNIQUE (org_id);  -- satu rekening aktif per org

ALTER TABLE payouts
    ADD COLUMN idempotency_key  text,
    ADD COLUMN account_snapshot jsonb NOT NULL DEFAULT '{}',  -- bank, nama, last4 saja (tanpa nomor lengkap)
    ADD COLUMN reject_reason    text,
    ADD COLUMN fail_reason      text,
    ADD COLUMN attempts         int NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at  timestamptz,
    ADD COLUMN approved_at      timestamptz,
    ADD COLUMN completed_at     timestamptz,
    ADD CONSTRAINT payouts_maker_checker CHECK (approved_by IS NULL OR approved_by <> requested_by);
CREATE UNIQUE INDEX payouts_idempotency_uniq ON payouts (org_id, idempotency_key);
-- Maksimal satu pencairan berjalan per org.
CREATE UNIQUE INDEX payouts_active_uniq ON payouts (org_id) WHERE status IN ('requested', 'approved', 'processing');
CREATE INDEX payouts_status_idx ON payouts (status, created_at);

CREATE TABLE refunds (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         uuid NOT NULL REFERENCES orders(id),
    org_id           uuid NOT NULL REFERENCES organizations(id),
    amount           bigint NOT NULL CHECK (amount > 0),
    status           text NOT NULL DEFAULT 'requested'
                     CHECK (status IN ('requested', 'approved', 'processing', 'manual_required', 'completed', 'rejected', 'failed')),
    reason           text NOT NULL,
    requested_by     uuid NOT NULL REFERENCES users(id),
    approved_by      uuid REFERENCES users(id),
    reference        text,          -- id refund gateway atau nomor referensi transfer manual
    reject_reason    text,
    fail_reason      text,
    attempts         int NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz,
    approved_at      timestamptz,
    completed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
-- Satu order hanya punya satu refund yang belum ditolak.
CREATE UNIQUE INDEX refunds_order_active_uniq ON refunds (order_id) WHERE status <> 'rejected';
CREATE INDEX refunds_status_idx ON refunds (status, created_at);
CREATE INDEX refunds_org_idx ON refunds (org_id, created_at DESC);

CREATE INDEX ledger_journals_ref_idx ON ledger_journals (ref_id);

-- Ledger append-only: koreksi selalu jurnal baru, baris lama tidak pernah diubah/dihapus.
CREATE FUNCTION ledger_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger append-only: % pada % ditolak', TG_OP, TG_TABLE_NAME;
END $$;
CREATE TRIGGER ledger_journals_immutable BEFORE UPDATE OR DELETE ON ledger_journals
    FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER ledger_entries_immutable BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER ledger_journals_no_truncate BEFORE TRUNCATE ON ledger_journals
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER ledger_entries_no_truncate BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_immutable();
