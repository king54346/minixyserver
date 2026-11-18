-- Actual path: CWarWarView.showPetDetail -> pet_mgr.translateWarPetInfo ->
-- PetEasyDetailView.CPetEasyDetailStatusView.setData/refreshWidgets.
local mode=arg[1] or 'direct'
assert(mode=='direct','burn display is maintained in pet_mgr source')
local source='../client2/apk-src/assets/script/'
local utils={string_strip=function(s)return s:match('^%s*(.-)%s*$')end,
 string_split_str=function(s)local r={};for x in s:gmatch('[^,]+')do r[#r+1]=x end;return r end}
_G.utils=utils
package.loaded.utils=utils;package.loaded.ui={};package.loaded.uicreator={}
for _,n in ipairs({'config','sig','auto.pet','auto.pet_upgrade_relation','auto.fight_pet','logic.magic_mgr','logic.luandou_mgr','logic.dao_mgr','war.effect','logic.record_mgr','auto.fightdlg','logic.zhenfa_mgr'})do package.loaded[n]={} end
CFG={usingTrunk=function()return false end};LOCAL_TXT=function(s)return s end;STR_FORMAT=string.format
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
game={};ui_mgr={};netwar={hideCurWar=function()end};package.loaded['war.netwar']=netwar
zhenfa_mgr={getFightPosDesc=function()return '蛇蟠阵三阶：受到法术伤害结果处减少936；' end}
local function info(id)
 if id==5024 then return {name='燃烧4',desc='回合结束时损失相当于当前上限$a%的生命值，持续到战斗结束。',param='8'} end
 if id==5025 then return {name='燃烧5',desc='回合结束时损失相当于当前上限$a%的生命值，持续到战斗结束。',param='10'} end
 return {name='其他状态'..id,desc='原有状态',param=''}
end
buff_mgr={createInfo=info}
assert(loadfile(source..'logic/pet_mgr.lua'))()
pet_mgr.newPet=function(t)return {type=t.type}end;pet_mgr.newFightPet=pet_mgr.newPet
pet_mgr.analyzePetTalentInfo=function()return {} end
war={war={CurrWar={getZhenFaInfo=function()return {zhenfa_type=1,vs_zhenfa_type=0,big_lvl=3}end,isZhenFaEyeDie=function()return false end}}}
assert(loadfile(source..'view/PetEasyDetailView.lua'))('view.PetEasyDetailView')
assert(loadfile(source..'view/WarWarView.lua'))('view.WarWarView')
local Status=view.PetEasyDetailView.CPetEasyDetailStatusView
local text
local label={setText=function(_,s)text=s end,getContentSize=function()return 550,150 end,
 setSize=function()end,setPosition=function()end,getPositionX=function()return 0 end}
local status=setmetatable({eblDesc=label,getWidth=function()return 550 end,setSize=function()end},{__index=Status})
ui_mgr.open=function(name)
 assert(name=='view.PetEasyDetailView.CPetEasyDetailMainView')
 return {setData=function(_,data)status:setData(data)end}
end
local C=view.WarWarView.CWarWarView
local function check(data,n4,n5)
 C.showPetDetail({},data)
 local _,a=text:gsub('燃烧4','');local _,b=text:gsub('燃烧5','')
 assert(a==n4 and b==n5,'actual battle click path collapsed burn layers: '..text)
 assert(text:find('蛇蟠阵三阶',1,true),'formation text lost')
 assert(text:find('蓄力',1,true),'power text lost')
 if n4>1 then assert(text:find('燃烧4（第1层）',1,true) and text:find('燃烧4（第2层）',1,true),'layer labels missing')end
end
local data={type=25024,pos=104,att=100,baseatt=100,hp=1000,fullHp=1000,spd=10,basespd=10,
 cardinfo={[2]='100',[3]='0'},shieldData=0,magicShieldData=0,powerFull=10,
 buffs={[3]={mid=5024,last=10},[4]={mid=5024,last=10},[5]={mid=5025,last=10}}}
local original=data.buffs
check(data,2,1);check(data,2,1)
assert(data.buffs==original and data.buffs[3].mid==5024 and data.buffs[4].mid==5024,'translator mutated battle slots')
data.buffs[4]=nil;check(data,1,1)
if arg[2] then
 local live=dofile(arg[2]);local n4,n5=0,0
 for _,b in pairs(live.buffs)do if b.mid==5024 then n4=n4+1 elseif b.mid==5025 then n5=n5+1 end end
 check(live,n4,n5)
 print('PASS: captured live battle slots through the actual UI path')
end
print('PASS: '..mode..'; shipped WarWarView -> actual translator -> shipped PetEasyDetail status widget; duplicate/mixed burns, removal, repeated opens, formation and battle state')
