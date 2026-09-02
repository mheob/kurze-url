-- Reads the cached link and, when one is present, records the visitor in the
-- link's daily unique-visitor set — in a single Redis command, because the
-- free tier's command budget is the binding constraint on the redirect path.
--
-- The cached value is "<link id>|<json>", so the script can find the link id
-- (which it needs to build the set key) without parsing JSON. The link id is
-- a UUID and never contains a pipe, so splitting on the first one is safe even
-- when the destination URL contains pipes.
--
-- KEYS[1] link cache key
-- ARGV[1] unique-visitor set prefix
-- ARGV[2] visitor hash
-- ARGV[3] day, as YYYY-MM-DD
-- ARGV[4] unique-visitor set TTL in seconds
-- returns { cached value or false, isUniqueVisit (0|1) }

local cached = redis.call('GET', KEYS[1])
if not cached then
  return { false, 0 }
end

if cached == '-' then
  return { cached, 0 }
end

local separator = string.find(cached, '|', 1, true)
if not separator then
  return { cached, 0 }
end

local linkID = string.sub(cached, 1, separator - 1)
local setKey = ARGV[1] .. linkID .. ':' .. ARGV[3]

local added = redis.call('SADD', setKey, ARGV[2])
if added == 1 then
  redis.call('EXPIRE', setKey, tonumber(ARGV[4]))
end

return { cached, added }
