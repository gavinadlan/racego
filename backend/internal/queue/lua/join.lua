-- Masuk antrian. Satu posisi per akun (ZADD NX); bergabung ulang tidak mengubah urutan.
--
-- KEYS[1] antrian (ZSET, skor = urutan)   {ev:<id>}:queue
-- KEYS[2] tanda hidup (ZSET, skor = ms)    {ev:<id>}:seen
-- KEYS[3] pass aktif (ZSET, skor = exp ms) {ev:<id>}:active
-- KEYS[4] penanda urutan lobi dibekukan    {ev:<id>}:opened
-- ARGV[1] user id
-- ARGV[2] skor acak lobi (0 .. 2^52-1, dari crypto/rand)
-- ARGV[3] sekarang (unix ms)
-- ARGV[4] jam buka penjualan (unix ms)
--
-- Hasil: 1 baru bergabung | 0 sudah di antrian atau sedang memegang pass
local now = tonumber(ARGV[3])
local pass = redis.call('ZSCORE', KEYS[3], ARGV[1])
if pass and tonumber(pass) > now then
  return 0
end
if redis.call('ZSCORE', KEYS[1], ARGV[1]) then
  redis.call('ZADD', KEYS[2], now, ARGV[1])
  return 0
end
-- Peserta lobi: skor acak, jadi waktu masuk lobi tidak berpengaruh pada urutan.
-- Setelah jam buka: 2^52 + waktu, selalu di belakang semua peserta lobi dan urut kedatangan
-- (2^52 + ms masih bilangan bulat yang tepat di double, beda dengan 2^53 + ms).
local score = tonumber(ARGV[2])
if now >= tonumber(ARGV[4]) or redis.call('EXISTS', KEYS[4]) == 1 then
  score = 4503599627370496 + now
end
redis.call('ZADD', KEYS[1], 'NX', score, ARGV[1])
redis.call('ZADD', KEYS[2], now, ARGV[1])
-- Batas umur 7 hari (diperbarui tiap ada yang bergabung): antrian event yang ditinggal tanpa
-- status akhir (mis. ditarik ke draft) tidak menumpuk di Redis yang berjalan dengan noeviction.
redis.call('EXPIRE', KEYS[1], 604800)
redis.call('EXPIRE', KEYS[2], 604800)
return 1
