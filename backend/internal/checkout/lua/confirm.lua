-- Kunci slot secara permanen setelah pembayaran lunas.
--
-- KEYS[1] stok    KEYS[2] jumlah milik user    KEYS[3] indeks hold    KEYS[4] detail hold
-- KEYS[5] penanda sudah dikonfirmasi            {cat:<id>}:confirmed:<rid>
-- ARGV[1] id reservasi    ARGV[2] qty    ARGV[3] TTL penanda (detik)
--
-- Hasil:
--  1 hold masih aktif → slot dikunci (kasus normal)
--  2 hold sudah kedaluwarsa tapi stok masih ada → slot diambil ulang (bayar telat, tetap dapat)
--  3 sudah pernah dikonfirmasi (pemanggilan ulang, aman diabaikan)
--  0 hold kedaluwarsa dan stok habis → order harus di-refund
if redis.call('EXISTS', KEYS[5]) == 1 then
  return 3
end
if redis.call('ZREM', KEYS[3], ARGV[1]) == 1 then
  redis.call('DEL', KEYS[4])
  redis.call('SET', KEYS[5], '1', 'EX', ARGV[3])
  return 1
end
local qty = tonumber(ARGV[2])
local stock = tonumber(redis.call('GET', KEYS[1]) or '0')
if stock >= qty then
  redis.call('DECRBY', KEYS[1], qty)
  redis.call('INCRBY', KEYS[2], qty)
  redis.call('SET', KEYS[5], '1', 'EX', ARGV[3])
  return 2
end
return 0
