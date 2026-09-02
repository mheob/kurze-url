-- Reads the cached link and, when one is present and unprotected, records the
-- visitor in the link's daily unique-visitor set — in a single Redis command,
-- because the free tier's command budget is the binding constraint on the
-- redirect path.
--
-- The cached value is "<link id>|<0 or 1>|<json>", so the script can find the
-- link id (which it needs to build the set key) and the has-password flag
-- without parsing JSON. The link id is a UUID and never contains a pipe, so
-- splitting on the first two is safe even when the destination URL contains
-- pipes.
--
-- A password-protected link's click is only ever recorded once the password
-- verifies, on a separate path — marking the visitor unique here would
-- consume their uniqueness before that path runs, so the flag makes this
-- script skip the SADD/EXPIRE entirely and report the visit as not unique.
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

local sep1 = string.find(cached, '|', 1, true)
if not sep1 then
  return { cached, 0 }
end

local sep2 = string.find(cached, '|', sep1 + 1, true)
if not sep2 then
  return { cached, 0 }
end

local hasPassword = string.sub(cached, sep1 + 1, sep2 - 1)
if hasPassword == '1' then
  return { cached, 0 }
end

local linkID = string.sub(cached, 1, sep1 - 1)
local setKey = ARGV[1] .. linkID .. ':' .. ARGV[3]

local added = redis.call('SADD', setKey, ARGV[2])
if added == 1 then
  redis.call('EXPIRE', setKey, tonumber(ARGV[4]))
end

return { cached, added }
