-- Exercise the real loader rather than a mocked __onreq.
dofile('../client2/fixes/bootstrap.lua')
local calls, module = 0, {}
package.preload.loader_test = function() return module end
__onreq('loader_test', function(m)
  calls = calls + 1
  assert(require('loader_test') == m, 'reentrant require changed result')
end)
assert(require('loader_test') == module)
for i = 1, 10000 do require('loader_test') end
assert(calls == 1, 'cached reads reapplied the patch')
local late = 0
__onreq('loader_test', function(m) assert(m == module); late = late + 1 end)
assert(late == 1, 'late hook was not applied')
package.loaded.loader_test = nil
require('loader_test')
assert(calls == 2 and late == 2, 'same-table module reload was not patched')
module = {}
package.loaded.loader_test = nil
require('loader_test')
assert(calls == 3 and late == 3, 'replacement module was not patched')
local failures = 0
__onreq('loader_retry', function()
  failures = failures + 1
  if failures == 1 then error('expected installation failure') end
end)
package.preload.loader_retry = function() return {} end
require('loader_retry'); require('loader_retry'); require('loader_retry')
assert(failures == 2, 'failed installation did not recover')
package.preload.loader_alias = function() _G.loader_alias = {}; return true end
local alias = 0
__onreq('loader_alias', function(m) assert(m == _G.loader_alias); alias = alias + 1 end)
assert(require('loader_alias') == true)
require('loader_alias')
assert(alias == 1, 'Lua 5.1 module alias was not patched exactly once')
package.preload.loader_broken = function() error('original module failed') end
assert(not pcall(require, 'loader_broken'), 'original require error swallowed')
print('PASS: cached reads, reentrancy, late hooks, reloads, retries and aliases')
