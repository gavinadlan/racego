-- Kembalikan slot ke stok (hold kedaluwarsa, pembayaran expire/cancel).
-- Idempotent: hanya hold yang masih tercatat di indeks yang dikembalikan.
--
-- KEYS[1] stok    KEYS[2] jumlah milik user    KEYS[3] indeks hold    KEYS[4] detail hold
-- ARGV[1] id reservasi    ARGV[2] qty
--
-- Hasil: 1 dikembalikan | 0 sudah tidak ada (sudah dikembalikan / sudah dibayar)
if redis.call('ZREM', KEYS[3], ARGV[1]) == 0 then
  return 0
end
local qty = tonumber(ARGV[2])
redis.call('INCRBY', KEYS[1], qty)
local owned = tonumber(redis.call('GET', KEYS[2]) or '0')
if owned <= qty then
  redis.call('DEL', KEYS[2])
else
  redis.call('DECRBY', KEYS[2], qty)
end
redis.call('DEL', KEYS[4])
return 1
