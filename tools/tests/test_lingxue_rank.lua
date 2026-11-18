local timers,requests={},{}
utils={call_out=function(delay,fn)assert(delay==1 or delay==2);timers[#timers+1]={delay,fn}end}
package.loaded.utils=utils;package.loaded.ui={};package.loaded.uicreator={};package.loaded.sound_mgr={}
dao_mgr={C_OPEN_TOP=function(b)requests[#requests+1]=b end,C_OPEN_LUNDAO=function()requests[#requests+1]=0 end}
package.loaded['logic.dao_mgr']=dao_mgr
ui_mgr={};package.loaded.ui_mgr=ui_mgr
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end};game={}
assert(loadfile('../client2/apk-src/assets/script/view/RankView.lua'))('view.RankView')
function __onreq(name,fn)if name=='view.RankView'then fn(view.RankView)end end
dofile('../client2/fixes/rank_bands.lua');dofile('../client2/fixes/rank_bands.lua')
local rendered=0
local rank=setmetatable({curIdx=1,refreshWidgets=function()rendered=rendered+1 end},{__index=view.RankView.CRankMainView})
local source={type=30,list={{uid=1,grade=30},{uid=2,grade=35},{uid=1,grade=30}}}
rank:setData(source)
assert(#rank.data.list==1 and rank.data.list[1].uid==1 and #source.list==3,'old band retained higher grade/duplicate UID or changed cached source')
rank.curIdx=2;rank:setData(source);rank:setData(source)
assert(rendered==1 and #timers==1,'stale response rendered or duplicate retry queued')
local retry=table.remove(timers,1);assert(retry[1]==1);retry[2]()
assert(#requests==1 and requests[1]==40,'latest tab was not refetched')
rank:setData({type=40,list={{uid=2,grade=35},{uid=3,grade=41}}})
assert(#rank.data.list==1 and rank.data.list[1].uid==2,'wrong 40-level membership')
rank.curIdx=3;rank:setData({type=50,list={{uid=3,grade=41},{uid=2,grade=35}}})
assert(#rank.data.list==1 and rank.data.list[1].uid==3,'wrong 50-level membership')
print('PASS: shipped RankView setter, current-band filtering, UID deduplication, cached data preserved, late replies and one-shot refetch')

local function node(v)
 return {shown=v,isVisible=function(s)return s.shown end,setVisible=function(s,x)s.shown=x end}
end
local party,map=node(true),node(false)
ui_mgr.getUIList=function()return {{UI_FILE='Party_Main',inst=party},{UI_FILE='Party_LingXueMapBottom',inst=map}}end
dofile('../client2/fixes/lingxue_ui.lua');dofile('../client2/fixes/lingxue_ui.lua')
assert(#timers==1,'duplicate watcher installed')
local function tick()
 assert(#timers==1,'watcher queue grew')
 local t=table.remove(timers,1);assert(t[1]==2);t[2]()
end
tick();assert(party.shown,'hidden cached map closed a normal party view')
map.shown=true;tick();assert(not party.shown,'visible map remained covered')
map.shown=false;party.shown=true;tick();assert(party.shown,'party failed to reopen')
map.shown=true;tick();assert(not party.shown,'second participation failed')
for i=1,1000 do tick() end
party.shown=true;tick();assert(not party.shown,'fix stopped after thirty minutes')
ui_mgr.getUIList=function()return {Party_Main={UI_FILE='Party_Main',inst=node(true)},Party_LingXueMapBottom={UI_FILE='Party_LingXueMapBottom',inst={isVisible=function()error('disposed node')end,setVisible=function()end}}}end
tick();assert(#timers==1,'disposed node stopped future checks')
print('PASS: imported lingxue visibility behavior, normal party reopening, repeated participation, no timeout or duplicate timers, disposed-node recovery')
