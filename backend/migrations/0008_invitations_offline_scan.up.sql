-- Tugas 08: undangan staf lewat email + scanner offline.

-- Undangan anggota mitra. Token mentah hanya ada di email; yang disimpan SHA-256-nya.
-- Status dihitung: diterima (accepted_at), dicabut (revoked_at), kedaluwarsa (expires_at < now()), selain itu aktif.
CREATE TABLE org_invitations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email         text NOT NULL CHECK (email = lower(email)),
    role          text NOT NULL CHECK (role IN ('admin', 'finance', 'rpc_staff')),  -- owner tidak bisa diundang
    token_hash    bytea UNIQUE,  -- NULL setelah dibersihkan worker (30 hari)
    invited_by    uuid NOT NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    accepted_at   timestamptz,
    accepted_by   uuid REFERENCES users(id),
    revoked_at    timestamptz,
    last_sent_at  timestamptz NOT NULL DEFAULT now(),
    send_count    int NOT NULL DEFAULT 1
);
-- Satu undangan belum diterima/dicabut per (org, email). Undangan kedaluwarsa dicabut otomatis saat
-- diundang ulang (lihat partner/invitations.go), karena now() tidak boleh dipakai di indeks parsial.
CREATE UNIQUE INDEX org_invitations_active_uniq ON org_invitations (org_id, email) WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- Scan offline: idempoten per entri antrean perangkat, jam perangkat disimpan terpisah.
ALTER TABLE scan_logs
    ADD COLUMN client_op_id uuid,
    ADD COLUMN device_id uuid,
    ADD COLUMN scanned_at_device timestamptz,
    ADD COLUMN source text NOT NULL DEFAULT 'online' CHECK (source IN ('online', 'offline_sync'));
CREATE UNIQUE INDEX scan_logs_client_op_uniq ON scan_logs (client_op_id) WHERE client_op_id IS NOT NULL;

-- Pembaruan manifest inkremental (?since=): kapan status tiket terakhir berubah.
ALTER TABLE tickets ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX tickets_event_updated_idx ON tickets (event_id, updated_at);
-- Diisi otomatis bila UPDATE tidak menyetel updated_at sendiri.
CREATE FUNCTION tickets_touch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.updated_at IS NOT DISTINCT FROM OLD.updated_at THEN
        NEW.updated_at := clock_timestamp();
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER tickets_touch BEFORE UPDATE ON tickets FOR EACH ROW EXECUTE FUNCTION tickets_touch();
