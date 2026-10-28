-- Panggil peserta dari depan antrian. Dijalankan worker tiap detik; atomik, jadi aman
-- walau beberapa instance worker berjalan bersamaan.
--
-- KEYS[1] antrian   KEYS[2] tanda hidup   KEYS[3] pass aktif   KEYS[4] meta (HASH)   KEYS[5] penanda dibekukan
-- ARGV[1] sekarang (unix ms)
-- ARGV[2] laju panggil per detik
-- ARGV[3] pass aktif maksimum
-- ARGV[4] masa berlaku pass (ms)
-- ARGV[5] batas tanda hidup (ms): lebih lama dari ini tanpa polling = dibuang
-- ARGV[6] jam buka penjualan (unix ms)
--
-- Jatah panggil = min(laju × detik sejak panggilan terakhir, pass aktif maksimum − pass aktif).
-- Hasil: {jumlah dipanggil, jumlah dibuang, pass aktif setelahnya, user id yang dipanggil...}
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local maxActive = tonumber(ARGV[3])
local passTTL = tonumber(ARGV[4])
local seenAfter = now - tonumber(ARGV[5])
if now < tonumber(ARGV[6]) then
  return {0, 0, redis.call('ZCARD', KEYS[3])}
end
redis.call('SET', KEYS[5], ARGV[6], 'NX')
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now)

local last = tonumber(redis.call('HGET', KEYS[4], 'last_ms') or (now - 1000))
local elapsed = math.max(0, math.min(now - last, 1000))
local budget = tonumber(redis.call('HGET', KEYS[4], 'budget') or '0') + rate * elapsed / 1000
budget = math.min(budget, rate) -- jatah tidak menumpuk lebih dari satu detik
local n = math.min(math.floor(budget), maxActive - redis.call('ZCARD', KEYS[3]))

local admitted, dropped = {}, 0
while #admitted < n and dropped < 5000 do
  local batch = redis.call('ZRANGE', KEYS[1], 0, n - #admitted + 49)
  if #batch == 0 then
    break
  end
  for _, uid in ipairs(batch) do
    if #admitted >= n then
      break
    end
    redis.call('ZREM', KEYS[1], uid)
    local seen = redis.call('ZSCORE', KEYS[2], uid)
    redis.call('ZREM', KEYS[2], uid)
    if seen and tonumber(seen) >= seenAfter then
      redis.call('ZADD', KEYS[3], now + passTTL, uid)
      admitted[#admitted + 1] = uid
    else
      dropped = dropped + 1 -- tab ditutup / tidak polling lagi: lewati, slot tidak termakan
    end
  end
end

budget = budget - #admitted
local total = redis.call('HINCRBY', KEYS[4], 'admitted_total', #admitted)
redis.call('HSET', KEYS[4], 'last_ms', now, 'budget', tostring(budget))

-- Laju panggil terukur (untuk perkiraan waktu tunggu): rata-rata per jendela 15 detik, dihaluskan.
local markMs = tonumber(redis.call('HGET', KEYS[4], 'mark_ms') or '0')
if markMs == 0 then
  redis.call('HSET', KEYS[4], 'mark_ms', now, 'mark_total', total - #admitted)
elseif now - markMs >= 15000 then
  local sample = (total - tonumber(redis.call('HGET', KEYS[4], 'mark_total'))) * 1000 / (now - markMs)
  local old = redis.call('HGET', KEYS[4], 'rate')
  local smoothed = sample
  if old then
    smoothed = (tonumber(old) + sample) / 2
  end
  redis.call('HSET', KEYS[4], 'rate', tostring(smoothed), 'mark_ms', now, 'mark_total', total)
end

-- Batas umur 7 hari, diperbarui tiap putaran (lihat join.lua).
for i = 1, 5 do
  redis.call('EXPIRE', KEYS[i], 604800)
end

local out = {#admitted, dropped, redis.call('ZCARD', KEYS[3])}
for _, uid in ipairs(admitted) do
  out[#out + 1] = uid
end
return out
