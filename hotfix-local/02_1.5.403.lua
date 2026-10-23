-- 本服 hotfix：纹理缓存告警（main.TextureCacheMemoryWarning → game.clearUIView）会调
-- signal.clearUISignals() 清掉所有界面信号，连刚挂上、正在等回包的「等待锁」回调也一起清了
-- （例：装备 / 法术强化点确定时打开提示框加载图片触发告警，S_UPDATE_SUIT / S_UPDATE_BOOK
-- 回来后没人处理，界面不复位、已消耗的材料仍显示为已选）。
-- 这里记下 regUI(..., {lock = true}) 挂上的回调，清界面信号后把还没等到回包的重新挂回去。
if rawget(signal, "__local_keep_lock") then return end
signal.__local_keep_lock = true
local pending = {} -- sig -> { {fn, opt}, ... }
local reg, dispatch, clear = signal.reg, signal.dispatch, signal.clearUISignals
signal.reg = function(sig, fn, opt)
  if opt and opt.ui and opt.lock and not opt.forever then
    pending[sig] = pending[sig] or {}
    table.insert(pending[sig], {fn, opt})
  end
  return reg(sig, fn, opt)
end
signal.dispatch = function(sig, ...)
  pending[sig] = nil
  return dispatch(sig, ...)
end
signal.clearUISignals = function(...)
  local keep = pending
  pending = {}
  local r = clear(...)
  for sig, list in pairs(keep) do
    for _, e in ipairs(list) do
      e[2].funcIdx = nil -- 旧编号已随清理作废，重新分配
      signal.reg(sig, e[1], e[2])
    end
  end
  return r
end
print("[LOCAL HOTFIX] keep pending lock signals installed")
