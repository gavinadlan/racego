-- Ubah stok sebesar selisih kuota (kuota baru − kuota lama) secara ATOMIK.
--
-- Sengaja memakai selisih, bukan menimpa angka absolut: di saat yang sama pembeli bisa sedang
-- memotong stok (reserve.lua) atau slot kembali (release.lua). Karena Redis menjalankan skrip
-- ini tanpa disela, cek "stok + selisih >= 0" sama dengan "kuota baru >= slot terpakai"
-- (terjual + sedang ditahan) tepat pada saat itu, jadi total reservasi tidak pernah
-- melebihi kuota baru.
--
-- KEYS[1] stok kategori    {cat:<id>}:stock
-- ARGV[1] selisih (boleh negatif)
--
-- Hasil: {1, stok baru} | {0, stok sekarang} bila stok akan negatif | {-1, 0} bila stok belum dimuat
local stock = redis.call('GET', KEYS[1])
if not stock then
  return {-1, 0}
end
stock = tonumber(stock)
local delta = tonumber(ARGV[1])
if stock + delta < 0 then
  return {0, stock}
end
return {1, redis.call('INCRBY', KEYS[1], delta)}
