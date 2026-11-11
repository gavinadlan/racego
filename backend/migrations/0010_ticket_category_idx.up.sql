-- Pagar kuota di database (Tugas 11): IssueForOrder menghitung tiket per kategori di dalam transaksi
-- yang mengunci baris kategori. Indeks ini membuat hitungan itu murah.
CREATE INDEX tickets_category_idx ON tickets (category_id);
