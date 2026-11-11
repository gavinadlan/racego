-- Tugas 10: absen hari-H + undian doorprize (docs/12-undian.md).

-- Absen hari-H: terpisah dari status tiket dan dari pengambilan race pack.
ALTER TABLE tickets
    ADD COLUMN attended_at timestamptz,
    ADD COLUMN attended_by uuid REFERENCES users(id);
-- Kolam undian dan ringkasan hadir membaca tiket yang hadir per event.
CREATE INDEX tickets_attended_idx ON tickets (event_id) WHERE attended_at IS NOT NULL;

-- scan_logs lama = pengambilan race pack.
ALTER TABLE scan_logs ADD COLUMN kind text NOT NULL DEFAULT 'collect' CHECK (kind IN ('collect', 'attend'));

-- Pengaturan event. Jendela absen kosong = bawaan (hari lomba WIB sampai akhir hari berikutnya).
ALTER TABLE events
    ADD COLUMN attendance_opens_at  timestamptz,
    ADD COLUMN attendance_closes_at timestamptz,
    ADD COLUMN draw_eligibility     text    NOT NULL DEFAULT 'attended' CHECK (draw_eligibility IN ('attended', 'collected')),
    ADD COLUMN allow_multiple_wins  boolean NOT NULL DEFAULT false,
    ADD COLUMN draw_public          boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT events_attendance_window CHECK (attendance_opens_at IS NULL OR attendance_closes_at IS NULL OR attendance_closes_at > attendance_opens_at);

-- Hadiah undian. Hadiah yang sudah punya pemenang tidak bisa dihapus (dicek di aplikasi + FK RESTRICT).
CREATE TABLE draw_prizes (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id     uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    org_id       uuid NOT NULL REFERENCES organizations(id),
    name         text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100),
    quantity     int  NOT NULL CHECK (quantity BETWEEN 1 AND 1000),
    category_id  uuid REFERENCES ticket_categories(id) ON DELETE SET NULL,
    position     int  NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX draw_prizes_event_idx ON draw_prizes (event_id, position, created_at);

-- Pemenang: append-only. Tidak ada UPDATE selain status/catatan, tidak ada DELETE (trigger di bawah).
-- Pengganti = baris baru. Satu tiket menang sekali per event bila allow_multiple_wins = false:
-- ditegakkan di transaksi undian (di bawah kunci advisory per event), karena bergantung pada pengaturan.
CREATE TABLE draw_winners (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    prize_id    uuid NOT NULL REFERENCES draw_prizes(id) ON DELETE RESTRICT,
    event_id    uuid NOT NULL REFERENCES events(id),
    org_id      uuid NOT NULL REFERENCES organizations(id),
    ticket_id   uuid NOT NULL REFERENCES tickets(id),
    seq         int  NOT NULL,                       -- urutan pengundian di dalam hadiah (1, 2, …)
    status      text NOT NULL DEFAULT 'winner' CHECK (status IN ('winner', 'claimed', 'forfeited')),
    drawn_at    timestamptz NOT NULL DEFAULT now(),
    drawn_by    uuid NOT NULL REFERENCES users(id),
    pool_size   int  NOT NULL CHECK (pool_size > 0),
    pool_hash   text NOT NULL,                       -- SHA-256 (hex) dari ticket_id kolam yang diurutkan
    request_key text,                                -- kunci per-klik klien: pengiriman ulang tidak mengundi dua kali
    note        text,                                -- alasan forfeited
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (prize_id, ticket_id),
    UNIQUE (prize_id, seq)
);
CREATE INDEX draw_winners_event_idx ON draw_winners (event_id, status);
CREATE INDEX draw_winners_ticket_idx ON draw_winners (ticket_id) WHERE status IN ('winner', 'claimed');
CREATE UNIQUE INDEX draw_winners_request_uniq ON draw_winners (prize_id, request_key, seq) WHERE request_key IS NOT NULL;

CREATE FUNCTION draw_winners_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'draw_winners append-only: baris pemenang tidak boleh dihapus';
    END IF;
    IF NEW.prize_id <> OLD.prize_id OR NEW.ticket_id <> OLD.ticket_id OR NEW.seq <> OLD.seq
       OR NEW.drawn_at <> OLD.drawn_at OR NEW.drawn_by <> OLD.drawn_by
       OR NEW.pool_size <> OLD.pool_size OR NEW.pool_hash <> OLD.pool_hash THEN
        RAISE EXCEPTION 'draw_winners append-only: hanya status dan catatan yang boleh berubah';
    END IF;
    IF OLD.status = 'forfeited' AND NEW.status <> 'forfeited' THEN
        RAISE EXCEPTION 'draw_winners: pemenang yang gugur tidak bisa dihidupkan lagi (undi pengganti)';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END $$;
CREATE TRIGGER draw_winners_guard BEFORE UPDATE OR DELETE ON draw_winners FOR EACH ROW EXECUTE FUNCTION draw_winners_guard();
