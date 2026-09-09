-- Write-only half: records one event in the current window. See
-- ratelimit_peek.lua for why the two are separate, and why the slot
-- arithmetic here must stay identical to it and to ratelimit.lua.
--
-- KEYS[1] base key
-- ARGV[1] window length in seconds
-- ARGV[2] current time in unix milliseconds
-- returns the current window's count after the increment

local window = tonumber(ARGV[1])
local now    = tonumber(ARGV[2])

local windowMillis = window * 1000
local currentKey   = KEYS[1] .. ':' .. math.floor(now / windowMillis)

local count = redis.call('INCR', currentKey)
redis.call('EXPIRE', currentKey, window * 2)

return count
