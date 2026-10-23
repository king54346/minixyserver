-- 本服 hotfix：每天活跃度满 125 领最后一个宝箱时服务端额外送 10 万元宝（xymsrv/tasks.go activeFullGold），
-- 宝箱预览（view.DailyView.getDailyRewardData）同步显示 100050 元宝。
if not rawget(_G, "__onreq") then return end
__onreq("view.DailyView", function(m)
if rawget(m, "__local_active_gold") then return end
m.__local_active_gold = true
local old = m.getDailyRewardData
m.getDailyRewardData = function(i)
  local d = old(i)
  if i == 5 and type(d) == "table" then
    local c = {}
    for k, v in pairs(d) do c[k] = v end
    c.gold = 100050
    return c
  end
  return d
end
end)
