-- Skema awal RaceGo. Semua nominal uang dalam BIGINT rupiah (tanpa desimal).
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ===== Akun global (pelari & staf) =====
CREATE TABLE users (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       text NOT NULL UNIQUE,
    name        text NOT NULL,
    phone       text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE runner_profiles (
    user_id          uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    id_number        text,        -- NIK/paspor; enkripsi kolom sebelum produksi
    birth_date       date,
    gender           text CHECK (gender IN ('M', 'F')),
    blood_type       text,
    jersey_size      text,
    emergency_name   text,
    emergency_phone  text,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- ===== Tenant (mitra/penyelenggara) =====
CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL UNIQUE,
    name        text NOT NULL,
    kyc_status  text NOT NULL DEFAULT 'pending' CHECK (kyc_status IN ('pending', 'verified', 'rejected')),
    fee_bps     int  NOT NULL DEFAULT 300 CHECK (fee_bps BETWEEN 0 AND 10000), -- 300 = 3%
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE org_members (
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        text NOT NULL CHECK (role IN ('owner', 'admin', 'finance', 'rpc_staff')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX org_members_user_idx ON org_members (user_id);

CREATE TABLE payout_accounts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    bank_code       text NOT NULL,
    account_number  text NOT NULL,
    account_name    text NOT NULL,
    verified_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- ===== Event & kategori =====
CREATE TABLE events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         uuid NOT NULL REFERENCES organizations(id),
    slug           text NOT NULL,
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    venue          text NOT NULL DEFAULT '',
    city           text NOT NULL DEFAULT '',
    race_date      date NOT NULL,
    sale_open_at   timestamptz NOT NULL,
    sale_close_at  timestamptz NOT NULL,
    status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'review', 'published', 'closed')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX events_status_idx ON events (status, race_date);

CREATE TABLE ticket_categories (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id      uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    org_id        uuid NOT NULL REFERENCES organizations(id),
    code          text NOT NULL,             -- 5K, 10K, 21K, 42K
    name          text NOT NULL,
    distance_km   numeric(5,2) NOT NULL,
    price         bigint NOT NULL CHECK (price >= 0),
    quota         int NOT NULL CHECK (quota >= 0),
    max_per_user  int NOT NULL DEFAULT 1 CHECK (max_per_user > 0),
    bib_prefix    text NOT NULL,             -- mis. "5" → BIB 50001
    bib_next      int NOT NULL DEFAULT 1,
    UNIQUE (event_id, code)
);

-- ===== Order & peserta =====
CREATE TABLE orders (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations(id),
    event_id         uuid NOT NULL REFERENCES events(id),
    category_id      uuid NOT NULL REFERENCES ticket_categories(id),
    buyer_id         uuid NOT NULL REFERENCES users(id),
    reservation_id   text NOT NULL UNIQUE,
    idempotency_key  text NOT NULL,
    qty              int NOT NULL CHECK (qty > 0),
    subtotal         bigint NOT NULL,
    platform_fee     bigint NOT NULL,
    total            bigint NOT NULL,
    status           text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'paid', 'expired', 'cancelled', 'refunded', 'needs_refund')),
    snap_token       text,
    payment_url      text,
    expires_at       timestamptz NOT NULL,
    paid_at          timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (buyer_id, idempotency_key)
);
CREATE INDEX orders_pending_expiry_idx ON orders (expires_at) WHERE status = 'pending';
CREATE INDEX orders_org_idx ON orders (org_id, event_id, status);

CREATE TABLE participants (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    org_id           uuid NOT NULL REFERENCES organizations(id),
    event_id         uuid NOT NULL REFERENCES events(id),
    category_id      uuid NOT NULL REFERENCES ticket_categories(id),
    full_name        text NOT NULL,
    email            text NOT NULL,
    id_number        text NOT NULL,
    birth_date       date,
    gender           text,
    jersey_size      text NOT NULL,
    emergency_name   text NOT NULL DEFAULT '',
    emergency_phone  text NOT NULL DEFAULT ''
);
CREATE INDEX participants_order_idx ON participants (order_id);

-- ===== Tiket =====
CREATE TABLE tickets (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    participant_id     uuid NOT NULL UNIQUE REFERENCES participants(id), -- 1 tiket per peserta (idempotent)
    order_id           uuid NOT NULL REFERENCES orders(id),
    org_id             uuid NOT NULL REFERENCES organizations(id),
    event_id           uuid NOT NULL REFERENCES events(id),
    category_id        uuid NOT NULL REFERENCES ticket_categories(id),
    bib_number         text NOT NULL,
    qr_token           text NOT NULL,
    status             text NOT NULL DEFAULT 'issued' CHECK (status IN ('issued', 'collected', 'cancelled', 'refunded')),
    collected_at       timestamptz,
    collected_by       uuid REFERENCES users(id),
    collected_by_name  text,   -- nama pengambil bila diwakilkan
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (category_id, bib_number)
);
CREATE INDEX tickets_event_idx ON tickets (event_id, status);

CREATE TABLE scan_logs (
    id          bigserial PRIMARY KEY,
    org_id      uuid NOT NULL,
    event_id    uuid NOT NULL,
    ticket_id   uuid,
    staff_id    uuid NOT NULL,
    result      text NOT NULL,  -- ok, already_collected, invalid, wrong_event, ...
    scanned_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scan_logs_event_idx ON scan_logs (event_id, scanned_at DESC);

-- ===== Pembayaran =====
-- Satu baris per (order, status) → notifikasi Midtrans yang dikirim ulang tidak diproses dua kali.
CREATE TABLE payment_events (
    id                  bigserial PRIMARY KEY,
    order_id            uuid NOT NULL,
    transaction_id      text,
    transaction_status  text NOT NULL,
    fraud_status        text,
    status_code         text,
    gross_amount        text,
    payment_type        text,
    payload             jsonb NOT NULL,
    received_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (order_id, transaction_status)
);

-- Transactional outbox: ditulis di transaksi yang sama dengan perubahan order, dibaca worker.
CREATE TABLE outbox (
    id            bigserial PRIMARY KEY,
    topic         text NOT NULL,
    payload       jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    attempts      int NOT NULL DEFAULT 0,
    available_at  timestamptz NOT NULL DEFAULT now(),  -- retry berikutnya (backoff)
    last_error    text,
    processed_at  timestamptz
);
CREATE INDEX outbox_pending_idx ON outbox (available_at, id) WHERE processed_at IS NULL;

-- ===== Ledger double-entry (append-only) =====
CREATE TABLE ledger_journals (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ref_type    text NOT NULL,
    ref_id      text NOT NULL,
    memo        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (ref_type, ref_id)   -- jurnal untuk satu kejadian hanya sekali
);

CREATE TABLE ledger_entries (
    id          bigserial PRIMARY KEY,
    journal_id  uuid NOT NULL REFERENCES ledger_journals(id),
    org_id      uuid REFERENCES organizations(id),
    account     text NOT NULL,
    debit       bigint NOT NULL DEFAULT 0 CHECK (debit >= 0),
    credit      bigint NOT NULL DEFAULT 0 CHECK (credit >= 0),
    CHECK (debit = 0 OR credit = 0)
);
CREATE INDEX ledger_entries_org_account_idx ON ledger_entries (org_id, account);

CREATE TABLE payouts (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES organizations(id),
    payout_account_id  uuid NOT NULL REFERENCES payout_accounts(id),
    amount             bigint NOT NULL CHECK (amount > 0),
    status             text NOT NULL DEFAULT 'requested'
                       CHECK (status IN ('requested', 'approved', 'processing', 'completed', 'failed', 'rejected')),
    reference_no       text UNIQUE,  -- reference_no Midtrans Payouts
    requested_by       uuid NOT NULL REFERENCES users(id),
    approved_by        uuid REFERENCES users(id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id          bigserial PRIMARY KEY,
    actor_id    uuid,
    org_id      uuid,
    action      text NOT NULL,
    entity      text NOT NULL,
    entity_id   text,
    before      jsonb,
    after       jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
