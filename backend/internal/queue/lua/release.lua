-- Keluar antrian: hapus posisi, tanda hidup, dan pass (bila ada). Aman dipanggil berulang.
--
-- KEYS[1] antrian   KEYS[2] tanda hidup   KEYS[3] pass aktif
-- ARGV[1] user id
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
return redis.call('ZREM', KEYS[3], ARGV[1])
