-- 本服 hotfix 公共：客户端关界面时会卸载界面模块（utils.reloadWithClose 清 package.loaded），下次打开重新加载，
-- 登录时给模块打的补丁就丢了。__onreq(模块名, 补丁函数)：登记补丁，模块每次被（重新）加载时自动再打一次；
-- 已加载的立即打。补丁函数要自己判断是否打过（在模块 / 类表上 rawset 标记）。
if rawget(_G, "__onreq") then return end
local hooks = {}
local req = require
__onreq = function(name, fn)
  hooks[name] = fn
  local m = package.loaded[name]
  if type(m) == "table" then pcall(fn, m) end
end
require = function(name, ...)
  local m = req(name, ...)
  local fn = hooks[name]
  if fn and type(m) == "table" then pcall(fn, m) end
  return m
end
