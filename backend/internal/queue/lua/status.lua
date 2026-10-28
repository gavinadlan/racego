-- Status satu peserta sekaligus memperbarui tanda hidup (heartbeat).
--
-- KEYS[1] antrian   KEYS[2] tanda hidup   KEYS[3] pass aktif   KEYS[4] meta (HASH)   KEYS[5] status akhir
-- ARGV[1] user id   ARGV[2] sekarang (unix ms)
--
-- Hasil: {3, "sold_out"|"closed"} | {2, exp_ms} dipanggil | {1, rank, ukuran, laju} menunggu | {0, ukuran}
local final = redis.call('GET', KEYS[5])
if final then
  return {3, final}
end
local now = tonumber(ARGV[2])
local pass = redis.call('ZSCORE', KEYS[3], ARGV[1])
if pass and tonumber(pass) > now then
  return {2, pass}
end
local size = redis.call('ZCARD', KEYS[1])
local rank = redis.call('ZRANK', KEYS[1], ARGV[1])
if rank then
  redis.call('ZADD', KEYS[2], now, ARGV[1])
  return {1, rank, size, redis.call('HGET', KEYS[4], 'rate') or '0'}
end
return {0, size}
