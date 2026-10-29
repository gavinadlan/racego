-- Verifikasi mitra (KYC) + super admin platform. Lihat docs/02-arsitektur.md.

-- Super admin hanya bisa diberikan lewat perintah: go run ./cmd/migrate superadmin <email>.
ALTER TABLE users ADD COLUMN is_super_admin boolean NOT NULL DEFAULT false;

-- Satu baris = satu pengajuan. Kolom *_enc berisi ciphertext secretbox ("v1:…"); *_last4 untuk tampilan tersamar.
CREATE TABLE org_verifications (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    attempt          int  NOT NULL CHECK (attempt >= 1),
    status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'submitted', 'approved', 'rejected')),
    org_type         text CHECK (org_type IN ('perorangan', 'komunitas', 'badan_usaha')),
    legal_name       text NOT NULL DEFAULT '',
    address          text NOT NULL DEFAULT '',
    pic_name         text NOT NULL DEFAULT '',
    pic_phone        text NOT NULL DEFAULT '',
    pic_id_enc       text,
    pic_id_last4     text,
    npwp_enc         text,
    npwp_last4       text,
    bank_code        text NOT NULL DEFAULT '',
    bank_name        text NOT NULL DEFAULT '',
    account_holder   text NOT NULL DEFAULT '',
    account_enc      text,
    account_last4    text,
    consent_at       timestamptz,
    consent_version  text,
    submitted_at     timestamptz,
    reviewed_at      timestamptz,
    reviewed_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    reject_reason    text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, attempt)
);
-- Satu org hanya punya satu pengajuan aktif (draft atau submitted).
CREATE UNIQUE INDEX org_verifications_active_uniq ON org_verifications (org_id) WHERE status IN ('draft', 'submitted');
CREATE INDEX org_verifications_status_idx ON org_verifications (status, submitted_at);

-- Berkas dokumen terenkripsi di KYC_DIR. Pengajuan ulang menyalin baris (storage_key sama);
-- berkas fisik baru dihapus bila tidak ada lagi baris yang merujuknya.
CREATE TABLE org_documents (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    verification_id  uuid NOT NULL REFERENCES org_verifications(id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('ktp_pic', 'npwp', 'surat_keterangan', 'akta_pendirian', 'sk_kemenkumham', 'nib', 'surat_kuasa')),
    storage_key      text NOT NULL,
    original_name    text NOT NULL,
    content_type     text NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png', 'application/pdf')),
    size             int  NOT NULL,
    sha256           text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (verification_id, kind)
);
CREATE INDEX org_documents_storage_key_idx ON org_documents (storage_key);
CREATE INDEX org_documents_sha256_idx ON org_documents (sha256);

-- Email verifikasi dikirim ke owner org: org_members (org_id, role) sudah tercakup PK (org_id, user_id).
