-- Naikkan hitungan slot milik user ke nilai dari database bila Redis lebih rendah (kunci hilang saat Redis
-- crash/restart). Hanya MENAIKKAN: tidak pernah menurunkan, jadi aman dijalankan berulang.
--
-- KEYS[1] jumlah slot milik user   {cat:<id>}:user:<uid>
-- ARGV[1] nilai target (dari database)
-- ARGV[2] TTL minimal kunci (detik); hanya memperpanjang
--
-- Hasil: 1 dinaikkan | 0 sudah >= target
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur >= tonumber(ARGV[1]) then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'KEEPTTL')
if redis.call('TTL', KEYS[1]) < tonumber(ARGV[2]) then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return 1
