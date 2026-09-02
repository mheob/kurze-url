-- Sliding-window counter. Estimates the request rate from the current window's
-- counter plus a time-weighted share of the previous window's, which smooths
-- the burst a plain fixed window allows at a window boundary.
--
-- KEYS[1] base key (the two window counters are derived from it)
-- ARGV[1] limit
-- ARGV[2] window length in seconds
-- ARGV[3] current time in unix milliseconds
-- returns { allowed (0|1), remaining }

local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now    = tonumber(ARGV[3])

local windowMillis = window * 1000
local currentSlot  = math.floor(now / windowMillis)

local currentKey  = KEYS[1] .. ':' .. currentSlot
local previousKey = KEYS[1] .. ':' .. (currentSlot - 1)

local currentCount  = tonumber(redis.call('GET', currentKey))  or 0
local previousCount = tonumber(redis.call('GET', previousKey)) or 0

local elapsed  = (now % windowMillis) / windowMillis
local carried  = previousCount * (1 - elapsed)
local estimate = carried + currentCount

if estimate >= limit then
  return { 0, 0 }
end

currentCount = redis.call('INCR', currentKey)
redis.call('EXPIRE', currentKey, window * 2)

local remaining = math.floor(limit - (carried + currentCount))
if remaining < 0 then
  remaining = 0
end

return { 1, remaining }
