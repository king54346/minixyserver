-- 调试用，只在排查时加进 -hotfix（…,hotfix-debug）：记录 xpcall 吞掉的 Lua 错误，以及部分信号的注册 / 派发。
-- 注意：日志借 C_CHANGE_NAME 回传，本服一旦实现改名，这里要换别的通道。文件名里的版本号必须等于客户端
-- res.plist 的 Version（1.5.403），否则 hotfix.loadHotfix 不执行。
-- 写手机文件受存储权限限制，改为借 ITEM_PTO.C_CHANGE_NAME{name} 发回服务端（服务端不处理，只在 -debug 日志里记下）。
local function log(...)
  local text = os.date("%H:%M:%S ") .. table.concat({...}, " ")
  pcall(net.send_prot, PT.ITEM_PTO.C_CHANGE_NAME, {name = string.sub(text, 1, 1000)})
end
local old = _G.__G__TRACKBACK__
_G.__G__TRACKBACK__ = function(msg, ...)
  log("ERR", tostring(msg), debug.traceback("", 2))
  if old then return old(msg, ...) end
end
local watch = {}
for _, n in ipairs({"S_UPDATE_SUIT", "S_UPDATE_BOOK", "S_REMOVE_BOOK", "S_REMOVE_SUIT"}) do
  watch[PT.ITEM_PTO[n]] = n
end
local reg, dispatch, clear = signal.reg, signal.dispatch, signal.clearUISignals
signal.reg = function(sig, fn, opt)
  if watch[sig] then log("reg", watch[sig], tostring(opt and opt.ui), tostring(opt and opt.lock)) end
  return reg(sig, fn, opt)
end
signal.dispatch = function(sig, ...)
  if watch[sig] then log("dispatch", watch[sig]) end
  return dispatch(sig, ...)
end
signal.clearUISignals = function(...)
  log("clearUISignals", debug.traceback("", 2))
  return clear(...)
end
log("debug hotfix installed")
