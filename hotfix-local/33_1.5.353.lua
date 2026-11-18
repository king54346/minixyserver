do
-- 2026-11-15：小夫子「火炮之术」数值调整，同步客户端技能描述。
-- 服务端改动：伤害 10/16/24/30%、每先攻护法装弹 12 枚、敌方入阵固定 5 颗、
-- 天匠（激发）每 3 回合把弹丸重置为开场数量。客户端只改文案，战斗由服务端计算。
--
-- TalentInfo 是懒加载的（datatable_mgr.readInfoModule），所以不能等 logic.talent_mgr
-- 加载完再改——那时表还不存在。这里包住 readInfoModule，在第一次读到 TalentInfo 时
-- 立刻改，保证 UI 拿到的已经是新文案。
local IDS = {22155, 22156, 22157, 22158}
local DESC_OLD = "获得先攻护法数*8枚不稳定弹丸，每次攻击或敌方护法入阵后，对目标发射一颗弹丸，造成6/12/18/24%初始攻击力的生命流失。"
local DESC_NEW = "获得先攻护法数*12枚不稳定弹丸，每次攻击后对目标发射一颗弹丸，敌方护法入阵时发射五颗，造成10/16/24/30%初始攻击力的生命流失。"
local TJ_OLD = "敌方护法入阵时，额外向目标发射1/2/3/4/5颗弹丸"
local TJ_NEW = "天匠：每3回合将不稳定弹丸重置为开场数量。"
local function replace(text, from, to)
local i = string.find(text, from, 1, true)
if not i then return text end
return string.sub(text, 1, i - 1) .. to .. string.sub(text, i + #from)
end
local function patch_mod(mod)
if type(mod) ~= "table" or type(mod.get_list) ~= "function" then return end
local ok, list = pcall(mod.get_list)
if not ok or type(list) ~= "table" then return end
for _, id in ipairs(IDS) do
local row = rawget(list, id)
if type(row) == "table" then
if type(row.desc) == "string" then row.desc = replace(row.desc, DESC_OLD, DESC_NEW) end
if type(row.tjdesc) == "string" then row.tjdesc = replace(row.tjdesc, TJ_OLD, TJ_NEW) end
end
end
end
__onreq("logic.datatable_mgr", function(module)
local dm = rawget(_G, "datatable_mgr")
if type(dm) ~= "table" or type(dm.readInfoModule) ~= "function" then return end
if rawget(dm, "__huopao_desc") == dm.readInfoModule then return end
local read = dm.readInfoModule
local function hooked(name, ...)
local mod = read(name, ...)
if name == "TalentInfo" then patch_mod(mod) end
return mod
end
dm.readInfoModule = hooked
dm.__huopao_desc = hooked
patch_mod(rawget(_G, "TalentInfo")) -- 万一已经加载过，补一次
end)

end
