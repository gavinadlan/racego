-- Jepit stok ke batas atas: stok tidak pernah boleh lebih besar dari kuota − slot yang dipakai order
-- aktif (dihitung dari database). Hanya MENURUNKAN stok, tidak pernah menaikkan: salah arah paling
-- buruk = menjual lebih sedikit, bukan oversell. Dipakai worker setelah Redis kehilangan tulisan
-- (mis. crash dengan AOF everysec kehilangan ≤1 detik pengurangan stok).
--
-- KEYS[1] stok kategori    {cat:<id>}:stock
-- ARGV[1] batas atas stok
--
-- Hasil: {1, stok sebelumnya} bila diturunkan | {0, stok sekarang} bila sudah ≤ batas | {-1, 0} bila belum dimuat
local stock = redis.call('GET', KEYS[1])
if not stock then
  return {-1, 0}
end
stock = tonumber(stock)
local cap = math.max(tonumber(ARGV[1]), 0)
if stock > cap then
  redis.call('SET', KEYS[1], cap, 'KEEPTTL')
  return {1, stock}
end
return {0, stock}
