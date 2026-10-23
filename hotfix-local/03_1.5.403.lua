-- 本服 hotfix：客户端装备表里辟尘分光履（1511）的速度是 5，服务端按 7 结算（用户确认 7 是对的），
-- 界面显示改成和实际一致。
if rawget(datatable_mgr, "__local_suit_speed") then return end
datatable_mgr.__local_suit_speed = true
local list = datatable_mgr.getDataInfoListByMod("SuitInfo")
if list and list[1511] then
  list[1511].speed = 7
end
