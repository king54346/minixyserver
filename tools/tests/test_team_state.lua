-- Exercise the shipped team refresh method, then the maintained client fix.
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
utils={};ui={};game={};signal={reg=function()end};SIG={}
CFG={usingTrunk=function()return false end,RACE_HUMAN=1,RACE_GHOST=2,RACE_GOD=3,RACE_BEAST=4}
for _,name in ipairs({'uicreator','ui','utils','pet_mgr','view.CommonItem','action_utils','signal','SIG','view.CPetView','view.ChildrenDayMgr','view.Child23_Ghost','logic.guide','logic.pet_mgr','logic.relate_mgr','logic.zhenfa_mgr'})do package.loaded[name]={}end
local legacyCalls,realCalls,mainUpdates=0,0,0
local power=553368
logic={capacity={getAllFightPetCapWithAdd=function()legacyCalls=legacyCalls+1;return 1263836 end,
 getAllFightPetCapLikeServer=function()realCalls=realCalls+1;return power end}}
package.loaded['logic.capacity']=logic.capacity
-- The shipped main header independently calculates the same formula.
local main={updateFight=function(s)mainUpdates=mainUpdates+1;s.power=logic.capacity.getAllFightPetCapLikeServer() end}
ui_mgr={getInst=function(name)assert(name=='view.CMainView.CMainView');return main end}
zhenfa_mgr={is_on=false,IconData={[8]={icon=88}},getCurrentZhenFa=function()return 8 end,isZhenFaActivity=function()return true end}
assert(loadfile('../client2/apk-src/assets/script/view/CTeamView.lua'))('view.CTeamView')
local C=view.CTeamView.CTeamView
local v=setmetatable({data={},lblCapacity={setText=function(s,n)s.text=n end},
 ctlZhenFa={setData=function(s,d)s.data=d end},setZhenfaVisible=function(s,x)s.zhenfa=x end,
 reset=function(s)s.resets=(s.resets or 0)+1 end},{__index=C})
v:refreshWidgets();assert(v.lblCapacity.text==1263836 and legacyCalls==1,'baseline must reproduce the old formula')
dofile('../client2/fixes/bootstrap.lua')
dofile('../client2/fixes/team_state.lua');local wrapped=C.refreshWidgets
dofile('../client2/fixes/team_state.lua');assert(C.refreshWidgets==wrapped,'installed twice')
assert(require('view.CTeamView').CTeamView.refreshWidgets==wrapped,'real require hook changed the installed method')
legacyCalls,realCalls,mainUpdates=0,0,0
v:refreshWidgets()
assert(v.lblCapacity.text==553368 and v.curCapacity==553368 and main.power==553368,'initial team/header mismatch')
assert(legacyCalls==0 and realCalls==2 and mainUpdates==1,'team/header must both use the current formula')
power=777777;zhenfa_mgr.is_on=true;v:refreshWidgets()
assert(v.lblCapacity.text==power and main.power==power and v.zhenfa and v.ctlZhenFa.data.type==8,'switch/formations refresh changed')
v.data=nil;v:refreshWidgets();assert(v.resets==1 and realCalls==4,'empty data must follow original reset')
print('PASS: actual CTeamView refresh, screenshot mismatch reproduced, one shared formula, main header update, formations, empty state and idempotence')

-- The server's newly included party factor must match the actual Lua formula.
party_mgr={getPartyTrainLv=function(name)return ({human=5,hp=3})[name]or 0 end}
assert(loadfile('../client2/apk-src/assets/script/logic/capacity.lua'))('logic.capacity')
for race,want in pairs({[1]=1.01175,[2]=1.0055,[3]=1.0055,[4]=1.0055})do
 assert(math.abs(logic.capacity.getPartyTrainAddRate({race=race})-want)<1e-10,'party effect level mismatch')
end
print('PASS: shipped capacity party factor matches server effect-level cases')
