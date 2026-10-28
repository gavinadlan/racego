-- Reservasi slot secara ATOMIK. Redis menjalankan skrip ini tanpa disela,
-- jadi tidak mungkin dua pembeli mendapat slot terakhir yang sama (nol oversell).
--
-- KEYS[1] stok tersisa kategori         {cat:<id>}:stock
-- KEYS[2] jumlah slot milik user        {cat:<id>}:user:<uid>
-- KEYS[3] indeks hold (ZSET, skor=exp)  {cat:<id>}:holds
-- KEYS[4] detail hold                   {cat:<id>}:hold:<rid>
-- ARGV[1] qty
-- ARGV[2] batas slot per user
-- ARGV[3] waktu kedaluwarsa hold (unix detik)
-- ARGV[4] id reservasi
-- ARGV[5] nilai hold ("<uid>|<qty>")
-- ARGV[6] TTL key hold dalam detik (lebih panjang dari hold, untuk jaga-jaga)
--
-- Hasil: >=0 sisa stok | -1 sold out | -2 melebihi batas per user | -3 penjualan belum dibuka
local stock = redis.call('GET', KEYS[1])
if not stock then
  return -3
end
stock = tonumber(stock)
local owned = tonumber(redis.call('GET', KEYS[2]) or '0')
local qty = tonumber(ARGV[1])

if owned + qty > tonumber(ARGV[2]) then
  return -2
end
if stock < qty then
  return -1
end

redis.call('DECRBY', KEYS[1], qty)
redis.call('INCRBY', KEYS[2], qty)
redis.call('ZADD', KEYS[3], ARGV[3], ARGV[4])
redis.call('SET', KEYS[4], ARGV[5], 'EX', ARGV[6])
return stock - qty
