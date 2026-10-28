-- Antrian virtual per event (opsional). Lihat docs/03-war-tiket.md.
ALTER TABLE events ADD COLUMN waiting_room boolean NOT NULL DEFAULT false;

-- Halaman order mem-polling jumlah tiket terbit per order (tickets_issued).
CREATE INDEX tickets_order_idx ON tickets (order_id);
