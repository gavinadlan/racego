-- Data contoh untuk development. Aman dijalankan berulang (ON CONFLICT DO NOTHING).
-- Tanggal dibuat RELATIF terhadap hari ini, jadi kalender & arsip selalu terlihat hidup
-- kapan pun seed dijalankan. Lima mitra sengaja dibuat agar isolasi multi-tenant bisa diuji.

INSERT INTO users (id, email, name, phone) VALUES
  ('11111111-1111-1111-1111-111111111111', 'budi@racego.local', 'Budi Pelari',             '081200000001'),
  ('22222222-2222-2222-2222-222222222222', 'sari@racego.local', 'Sari (Owner Jakarta)',    '081200000002'),
  ('33333333-3333-3333-3333-333333333333', 'andi@racego.local', 'Andi (Staf RPC Jakarta)', '081200000003'),
  ('44444444-4444-4444-4444-444444444444', 'rina@racego.local', 'Rina (Owner Bandung)',    '081200000004')
ON CONFLICT DO NOTHING;

INSERT INTO runner_profiles (user_id, id_number, birth_date, gender, blood_type, jersey_size, emergency_name, emergency_phone) VALUES
  ('11111111-1111-1111-1111-111111111111', '3171000000000001', '1995-06-15', 'M', 'O', 'L', 'Ibu Budi', '081299999999')
ON CONFLICT DO NOTHING;

INSERT INTO organizations (id, slug, name, kyc_status, fee_bps, city, theme_color, website, instagram, description) VALUES
  ('aaaaaaaa-0000-0000-0000-000000000001', 'jakarta-runners', 'Jakarta Runners Club', 'verified', 300, 'Jakarta', '#FF5B23', 'https://jakartarunners.club', 'jakartarunners',
   'Komunitas lari kota sejak 2014. Rutin menggelar latihan Selasa malam di GBK dan beberapa race road tiap tahun.'),
  ('aaaaaaaa-0000-0000-0000-000000000002', 'bandung-trail', 'Bandung Trail Community', 'verified', 300, 'Bandung', '#1F8A5B', 'https://bandungtrail.id', 'bandungtrail',
   'Penggemar lari bukit dan trail di Bandung Raya. Spesialis rute tanjakan Dago, Lembang, dan Tahura.'),
  ('aaaaaaaa-0000-0000-0000-000000000003', 'lari-jogja', 'Lari Jogja Collective', 'verified', 300, 'Yogyakarta', '#2F4BD8', 'https://larijogja.id', 'larijogja',
   'Kolektif pelari Yogyakarta yang membawa race ke jalanan bersejarah: Malioboro, Kotagede, sampai lereng Merapi.'),
  ('aaaaaaaa-0000-0000-0000-000000000004', 'bali-coastal', 'Bali Coastal Run', 'verified', 300, 'Denpasar', '#0E8F9C', 'https://balicoastalrun.com', 'balicoastal',
   'Race pinggir pantai dan sawah di Bali, start sebelum matahari terbit supaya pelari tidak kepanasan.'),
  ('aaaaaaaa-0000-0000-0000-000000000005', 'surabaya-night', 'Surabaya Night Runners', 'verified', 300, 'Surabaya', '#7A3FE0', 'https://sbynightrunners.id', 'surabayanight',
   'Lari malam di Surabaya: jalanan lebih sejuk, kota lebih sepi, dan selalu ada kuliner di garis finish.')
ON CONFLICT DO NOTHING;

INSERT INTO org_members (org_id, user_id, role) VALUES
  ('aaaaaaaa-0000-0000-0000-000000000001', '22222222-2222-2222-2222-222222222222', 'owner'),
  ('aaaaaaaa-0000-0000-0000-000000000001', '33333333-3333-3333-3333-333333333333', 'rpc_staff'),
  ('aaaaaaaa-0000-0000-0000-000000000002', '44444444-4444-4444-4444-444444444444', 'owner')
ON CONFLICT DO NOTHING;

INSERT INTO events (id, org_id, slug, name, tagline, description, venue, city, race_date, start_time,
                    sale_open_at, sale_close_at, status, racepack_info) VALUES
  ('eeeeeeee-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', 'jakarta-city-run', 'Jakarta City Run', 'Sudirman–Thamrin saat car free day',
   'Sudirman–Thamrin saat car free day. Kategori 5K, 10K dan 21K, start pukul 05:00 dari Bundaran HI. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Bundaran HI', 'Jakarta', current_date + 60, '05:00', now() - interval '1 hour', (current_date + 57)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Bundaran HI, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000002', 'bandung-half-marathon', 'Bandung Half Marathon', 'Dago sampai Lembang, tanjakan khas Bandung',
   'Dago sampai Lembang, tanjakan khas Bandung. Kategori 21K dan 42K, start pukul 05:30 dari Gedung Sate. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Gedung Sate', 'Bandung', current_date + 90, '05:30', now() - interval '1 hour', (current_date + 87)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Gedung Sate, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000003', 'malioboro-night-run', 'Malioboro Night Run', 'Lari malam menyusuri Malioboro dan Titik Nol',
   'Lari malam menyusuri Malioboro dan Titik Nol. Kategori 5K dan 10K, start pukul 19:30 dari Titik Nol Kilometer. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Titik Nol Kilometer', 'Yogyakarta', current_date + 18, '19:30', now() - interval '1 hour', (current_date + 15)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Titik Nol Kilometer, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000004', 'aaaaaaaa-0000-0000-0000-000000000004', 'sanur-sunrise-half', 'Sanur Sunrise Half', 'Start 04:45, matahari terbit di kilometer 8',
   'Start 04:45, matahari terbit di kilometer 8. Kategori 10K dan 21K, start pukul 04:45 dari Pantai Sanur. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Pantai Sanur', 'Denpasar', current_date + 45, '04:45', now() - interval '1 hour', (current_date + 42)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Pantai Sanur, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000005', 'aaaaaaaa-0000-0000-0000-000000000005', 'suroboyo-midnight-10k', 'Suroboyo Midnight 10K', 'Flag-off tengah malam di Tugu Pahlawan',
   'Flag-off tengah malam di Tugu Pahlawan. Kategori 10K, start pukul 23:59 dari Tugu Pahlawan. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Tugu Pahlawan', 'Surabaya', current_date + 30, '23:59', now() + interval '2 days', (current_date + 27)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Tugu Pahlawan, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000006', 'aaaaaaaa-0000-0000-0000-000000000001', 'thamrin-marathon', 'Thamrin Marathon', 'Full marathon pertama yang menutup Thamrin penuh',
   'Full marathon pertama yang menutup Thamrin penuh. Kategori 21K dan 42K, start pukul 04:30 dari Monas. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Monas', 'Jakarta', current_date + 150, '04:30', now() + interval '20 days', (current_date + 147)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Monas, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000007', 'aaaaaaaa-0000-0000-0000-000000000002', 'lembang-trail', 'Lembang Trail', 'Hutan pinus, kebun teh, dan satu tanjakan yang terkenal',
   'Hutan pinus, kebun teh, dan satu tanjakan yang terkenal. Kategori 15K dan 25K, start pukul 06:00 dari Grafika Cikole. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Grafika Cikole', 'Bandung', current_date + 70, '06:00', now() - interval '1 hour', (current_date + 67)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Grafika Cikole, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000008', 'aaaaaaaa-0000-0000-0000-000000000003', 'kaliurang-uphill', 'Kaliurang Uphill 12K', '12 kilometer menanjak ke lereng Merapi',
   '12 kilometer menanjak ke lereng Merapi. Kategori 12K, start pukul 05:00 dari Tugu Jogja. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Tugu Jogja', 'Yogyakarta', current_date + 100, '05:00', now() + interval '10 days', (current_date + 97)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Tugu Jogja, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000009', 'aaaaaaaa-0000-0000-0000-000000000004', 'ubud-rice-field-run', 'Ubud Rice Field Run', 'Rute pematang sawah Tegallalang',
   'Rute pematang sawah Tegallalang. Kategori 5K dan 10K, start pukul 06:00 dari Tegallalang. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Tegallalang', 'Gianyar', current_date + 120, '06:00', now() - interval '1 hour', (current_date + 117)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Tegallalang, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000010', 'aaaaaaaa-0000-0000-0000-000000000005', 'kenjeran-beach-run', 'Kenjeran Beach Fun Run', '5K santai di tepi pantai, cocok untuk pemula',
   '5K santai di tepi pantai, cocok untuk pemula. Kategori 5K, start pukul 06:00 dari Pantai Kenjeran. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Pantai Kenjeran', 'Surabaya', current_date + 9, '06:00', now() - interval '1 hour', (current_date + 6)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Pantai Kenjeran, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000011', 'aaaaaaaa-0000-0000-0000-000000000001', 'gbk-pagi-10k', 'GBK Pagi 10K', 'Dua putaran ring road GBK, PB gampang dikejar',
   'Dua putaran ring road GBK, PB gampang dikejar. Kategori 5K dan 10K, start pukul 05:30 dari Plaza Timur GBK. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Plaza Timur GBK', 'Jakarta', current_date + 25, '05:30', now() - interval '1 hour', (current_date + 22)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Plaza Timur GBK, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000012', 'aaaaaaaa-0000-0000-0000-000000000001', 'jakarta-night-10k-2026', 'Jakarta Night 10K 2026', 'Edisi malam pertama di Kuningan',
   'Edisi malam pertama di Kuningan. Kategori 10K, start pukul 20:00 dari Kuningan City. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Kuningan City', 'Jakarta', current_date - 40, '20:00', now() - interval '100 days', (current_date - 43)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Kuningan City, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000013', 'aaaaaaaa-0000-0000-0000-000000000002', 'dago-uphill-2026', 'Dago Uphill 2026', 'Tanjakan Dago yang legendaris',
   'Tanjakan Dago yang legendaris. Kategori 10K, start pukul 05:30 dari Simpang Dago. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Simpang Dago', 'Bandung', current_date - 75, '05:30', now() - interval '135 days', (current_date - 78)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Simpang Dago, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000014', 'aaaaaaaa-0000-0000-0000-000000000003', 'jogja-heritage-half-2026', 'Jogja Heritage Half 2026', 'Kraton, Kotagede, dan Prawirotaman',
   'Kraton, Kotagede, dan Prawirotaman. Kategori 10K dan 21K, start pukul 05:00 dari Alun-alun Utara. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Alun-alun Utara', 'Yogyakarta', current_date - 120, '05:00', now() - interval '180 days', (current_date - 123)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Alun-alun Utara, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000015', 'aaaaaaaa-0000-0000-0000-000000000004', 'kuta-sunset-10k-2026', 'Kuta Sunset 10K 2026', 'Finish tepat saat matahari terbenam',
   'Finish tepat saat matahari terbenam. Kategori 10K, start pukul 16:30 dari Pantai Kuta. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Pantai Kuta', 'Badung', current_date - 160, '16:30', now() - interval '220 days', (current_date - 163)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Pantai Kuta, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000016', 'aaaaaaaa-0000-0000-0000-000000000005', 'surabaya-heroes-run-2025', 'Surabaya Heroes Run 2025', 'Menyambut Hari Pahlawan',
   'Menyambut Hari Pahlawan. Kategori 5K dan 10K, start pukul 05:00 dari Tugu Pahlawan. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Tugu Pahlawan', 'Surabaya', current_date - 355, '05:00', now() - interval '415 days', (current_date - 358)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Tugu Pahlawan, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.'),
  ('eeeeeeee-0000-0000-0000-000000000017', 'aaaaaaaa-0000-0000-0000-000000000001', 'ciliwung-river-5k-2025', 'Ciliwung River 5K 2025', 'Lari sambil bersih-bersih sungai',
   'Lari sambil bersih-bersih sungai. Kategori 5K, start pukul 06:00 dari Taman Tebet Eco Park. Water station tiap 2,5 km, medali finisher untuk semua kategori, dan pacer resmi di kategori terjauh.',
   'Taman Tebet Eco Park', 'Jakarta', current_date - 300, '06:00', now() - interval '360 days', (current_date - 303)::timestamptz, 'published',
   'Pengambilan race pack H-2 dan H-1 di area Taman Tebet Eco Park, pukul 10.00–20.00. Bawa QR tiket dan KTP; pengambilan diwakilkan wajib membawa surat kuasa.')
ON CONFLICT DO NOTHING;

INSERT INTO ticket_categories (id, event_id, org_id, code, name, distance_km, price, quota, max_per_user, bib_prefix) VALUES
  ('cccccccc-0000-0000-0000-000000000005', 'eeeeeeee-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', '5K', 'Fun Run 5K', 5.00, 150000, 500, 4, '5'),
  ('cccccccc-0000-0000-0000-000000000010', 'eeeeeeee-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', '10K', '10K', 10.00, 250000, 300, 2, '10'),
  ('cccccccc-0000-0000-0000-000000000021', 'eeeeeeee-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', '21K', 'Half Marathon', 21.10, 350000, 200, 1, '21'),
  ('cccccccc-0000-0000-0001-000000000021', 'eeeeeeee-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000002', '21K', 'Half Marathon', 21.10, 375000, 150, 1, '21'),
  ('cccccccc-0000-0000-0001-000000000042', 'eeeeeeee-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000002', '42K', 'Full Marathon', 42.20, 550000, 100, 1, '42'),
  ('cccccccc-0000-0000-0003-000000000005', 'eeeeeeee-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000003', '5K', '5K', 5.00, 140000, 800, 4, '5'),
  ('cccccccc-0000-0000-0003-000000000010', 'eeeeeeee-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000003', '10K', '10K', 10.00, 210000, 500, 2, '10'),
  ('cccccccc-0000-0000-0004-000000000010', 'eeeeeeee-0000-0000-0000-000000000004', 'aaaaaaaa-0000-0000-0000-000000000004', '10K', '10K', 10.00, 275000, 400, 2, '10'),
  ('cccccccc-0000-0000-0004-000000000021', 'eeeeeeee-0000-0000-0000-000000000004', 'aaaaaaaa-0000-0000-0000-000000000004', '21K', 'Half Marathon', 21.10, 425000, 300, 1, '21'),
  ('cccccccc-0000-0000-0005-000000000010', 'eeeeeeee-0000-0000-0000-000000000005', 'aaaaaaaa-0000-0000-0000-000000000005', '10K', '10K', 10.00, 200000, 600, 2, '10'),
  ('cccccccc-0000-0000-0006-000000000021', 'eeeeeeee-0000-0000-0000-000000000006', 'aaaaaaaa-0000-0000-0000-000000000001', '21K', 'Half Marathon', 21.10, 450000, 1500, 1, '21'),
  ('cccccccc-0000-0000-0006-000000000042', 'eeeeeeee-0000-0000-0000-000000000006', 'aaaaaaaa-0000-0000-0000-000000000001', '42K', 'Full Marathon', 42.20, 750000, 800, 1, '42'),
  ('cccccccc-0000-0000-0007-000000000015', 'eeeeeeee-0000-0000-0000-000000000007', 'aaaaaaaa-0000-0000-0000-000000000002', '15K', 'Trail 15K', 15.00, 325000, 250, 1, '15'),
  ('cccccccc-0000-0000-0007-000000000025', 'eeeeeeee-0000-0000-0000-000000000007', 'aaaaaaaa-0000-0000-0000-000000000002', '25K', 'Trail 25K', 25.00, 450000, 150, 1, '25'),
  ('cccccccc-0000-0000-0008-000000000012', 'eeeeeeee-0000-0000-0000-000000000008', 'aaaaaaaa-0000-0000-0000-000000000003', '12K', 'Uphill 12K', 12.00, 260000, 400, 1, '12'),
  ('cccccccc-0000-0000-0009-000000000005', 'eeeeeeee-0000-0000-0000-000000000009', 'aaaaaaaa-0000-0000-0000-000000000004', '5K', '5K', 5.00, 190000, 400, 4, '5'),
  ('cccccccc-0000-0000-0009-000000000010', 'eeeeeeee-0000-0000-0000-000000000009', 'aaaaaaaa-0000-0000-0000-000000000004', '10K', '10K', 10.00, 290000, 300, 2, '10'),
  ('cccccccc-0000-0000-0010-000000000005', 'eeeeeeee-0000-0000-0000-000000000010', 'aaaaaaaa-0000-0000-0000-000000000005', '5K', 'Fun Run 5K', 5.00, 99000, 1000, 4, '5'),
  ('cccccccc-0000-0000-0011-000000000005', 'eeeeeeee-0000-0000-0000-000000000011', 'aaaaaaaa-0000-0000-0000-000000000001', '5K', '5K', 5.00, 160000, 700, 4, '5'),
  ('cccccccc-0000-0000-0011-000000000010', 'eeeeeeee-0000-0000-0000-000000000011', 'aaaaaaaa-0000-0000-0000-000000000001', '10K', '10K', 10.00, 240000, 600, 2, '10'),
  ('cccccccc-0000-0000-0012-000000000010', 'eeeeeeee-0000-0000-0000-000000000012', 'aaaaaaaa-0000-0000-0000-000000000001', '10K', '10K', 10.00, 230000, 800, 2, '10'),
  ('cccccccc-0000-0000-0013-000000000010', 'eeeeeeee-0000-0000-0000-000000000013', 'aaaaaaaa-0000-0000-0000-000000000002', '10K', 'Uphill 10K', 10.00, 250000, 500, 1, '10'),
  ('cccccccc-0000-0000-0014-000000000010', 'eeeeeeee-0000-0000-0000-000000000014', 'aaaaaaaa-0000-0000-0000-000000000003', '10K', '10K', 10.00, 250000, 800, 2, '10'),
  ('cccccccc-0000-0000-0014-000000000021', 'eeeeeeee-0000-0000-0000-000000000014', 'aaaaaaaa-0000-0000-0000-000000000003', '21K', 'Half Marathon', 21.10, 380000, 600, 1, '21'),
  ('cccccccc-0000-0000-0015-000000000010', 'eeeeeeee-0000-0000-0000-000000000015', 'aaaaaaaa-0000-0000-0000-000000000004', '10K', '10K', 10.00, 260000, 500, 2, '10'),
  ('cccccccc-0000-0000-0016-000000000005', 'eeeeeeee-0000-0000-0000-000000000016', 'aaaaaaaa-0000-0000-0000-000000000005', '5K', '5K', 5.00, 120000, 1500, 4, '5'),
  ('cccccccc-0000-0000-0016-000000000010', 'eeeeeeee-0000-0000-0000-000000000016', 'aaaaaaaa-0000-0000-0000-000000000005', '10K', '10K', 10.00, 180000, 1000, 2, '10'),
  ('cccccccc-0000-0000-0017-000000000005', 'eeeeeeee-0000-0000-0000-000000000017', 'aaaaaaaa-0000-0000-0000-000000000001', '5K', '5K', 5.00, 90000, 600, 4, '5')
ON CONFLICT DO NOTHING;

