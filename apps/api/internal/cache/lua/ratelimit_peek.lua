-- Read-only half of the sliding window in ratelimit.lua: reports whether one
-- more event would stay inside the limit, without recording one. It exists
-- because a counter that must only count failures has to be consulted before
-- the expensive work and incremented after it, which Allow's single atomic
-- step cannot express.
--
-- The slot arithmetic below MUST stay identical to ratelimit.lua and
-- ratelimit_count.lua. A peek and an increment that disagreed about where a
-- window begins would count into a slot nobody reads, and the limit would
-- silently never fire.
--
-- KEYS[1] base key (the two window counters are derived from it)
-- ARGV[1] limit
-- ARGV[2] window length in seconds
-- ARGV[3] current time in unix milliseconds
-- returns 1 when one more event stays inside the limit, 0 when it would not

local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now    = tonumber(ARGV[3])

local windowMillis = window * 1000
local currentSlot  = math.floor(now / windowMillis)

local currentCount  = tonumber(redis.call('GET', KEYS[1] .. ':' .. currentSlot))       or 0
local previousCount = tonumber(redis.call('GET', KEYS[1] .. ':' .. (currentSlot - 1))) or 0

local elapsed = (now % windowMillis) / windowMillis

if previousCount * (1 - elapsed) + currentCount >= limit then
  return 0
end

return 1
