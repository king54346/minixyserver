-- Run the shipped SuitUpgradeView bytecode, including confirmation registration
-- and response callback; replace only native widgets/network and inventory IO.
local widgets={}
local function widget()
  return {setText=function()end,setVisible=function(s,v)s.visible=v end,
    setIconOpacity=function(s,v)s.opacity=v end,setData=function()end,
    setPercentage=function()end,getPercentage=function()return 0 end,setScale=function()end}
end
local function count(t)local n=0;for _ in pairs(t)do n=n+1 end;return n end
utils={count=count,keys=function(t)local a={};for k in pairs(t)do a[#a+1]=k end;return a end,
  deepcopy=function(t)local a={};for k,v in pairs(t)do a[k]=v end;return a end,
  vstr=function()return '' end,message_box=function()end}
package.loaded.utils=utils;package.loaded.ui={};package.loaded.uicreator={}
package.loaded['logic.capacity']=true;package.loaded['view.AskBoxNewStyleView']={show=function()error('unexpected rare prompt')end}
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
game={};ui_mgr={};CFG={ITK_SHOE='shoe'};MyData={grade=100000,xgrade=0}
LOCAL_TXT=function(s)return s end;STR_FORMAT=string.format
logic={multiteam={isOpened=function()return true end},capacity={getAllFightPetCapLikeServer=function()return 100 end}}
local inventory,irons,scans,rebuilds,sends,callbacks,preview={},{},0,0,{}, {},0
local mt={__lt=function(a,b)return (a.itemnum or a.suit_type)<(b.itemnum or b.suit_type)end,
  __index=function(t,k)if k=='wear_pos' and (t.worn or t.reserved and t.__disable_multiteam)then return 1 end end}
local function material(id,exp,extra)
  local d={itemnum=id,type=id,star=1,exp_value=exp,is_lock=0,kind='weapon',grade=1,exp=0}
  for k,v in pairs(extra or {})do d[k]=v end
  return setmetatable(d,mt)
end
suit_mgr={getAllSuits=function()return inventory end,getSuitIronList=function()return irons end,
  getAllSuitsByFilter=function(fn,sort)
    scans=scans+1;local a={};for _,d in pairs(inventory)do if fn(d)then a[#a+1]=d end end
    table.sort(a,sort);return a
  end,
  getSuitByNum=function(id)return inventory[id]end,getSuitUpgradeExp=function()return 100000 end,
  addExpCanUpgrade=function(s,exp)return s.grade+math.floor((s.exp+exp)/100000),(s.exp+exp)%100000,100000 end}
datatable_mgr={getDataInfoByType=function(_,id)return inventory[id] or irons[id]end,
  getDataInfoListByMod=function()return inventory end}
PT={ITEM_PTO={S_UPDATE_SUIT=1,C_REFINE_SUITS=2}}
signal={regUI=function(prot,fn,opts)assert(prot==1 and opts.lock);callbacks[#callbacks+1]=fn end}
net={send_prot=function(prot,data)assert(prot==2);sends[#sends+1]=data end}
sound_mgr={play_sound=function()end};common_action={delay=function()end}
action_utils={Remind=function()end};package.loaded.action_utils=action_utils
assert(loadfile('../client2/apk-src/assets/script/view/SuitUpgradeView.lua'))('view.SuitUpgradeView')
local m=view.SuitUpgradeView;local C=m.CSuitUpgradeView
function __onreq(name,fn)assert(name=='view.SuitUpgradeView');fn(m)end
local originalAuto=C.onClickBtnAuto
local function upvalues(fn)
  local out={};for i=1,30 do local k,v=debug.getupvalue(fn,i);if not k then break end;out[k]=v end;return out
end
local function inst()
  local v=setmetatable({data=inventory[90000],currCnt=0,currExp=0,isVisible=function()return true end}, {__index=C})
  for _,k in ipairs({'lblLv','lblExp','prgProgress','rblNextLv','lblname','lblSelectCnt','lblConsume','lblDesc2','ctlpreviewItem','boxAlphaMask'})do v[k]=widget()end
  v.ctlpreviewItem.setData=function()preview=preview+1 end
  v.getSuitDesc=function()return ''end
  v.esrvList={childItems={},setData=function()rebuilds=rebuilds+1 end}
  for i=1,6 do v.esrvList.childItems[i]={data=inventory[i],imgsure=widget(),ctlIcon=widget()}end
  v.getUpGradeMsg=function(s)s.upGradeMsg='ok'end
  v:setData(v.data)
  return v
end
local function seed(n)
  inventory={};irons={}
  for i=1,n do inventory[i]=material(i,i)end
  inventory[90000]=material(90000,0,{star=5,name='target'})
  inventory[1].is_lock=1;inventory[2].star=5;inventory[3].kind='shoe'
  inventory[4].worn=true;inventory[5].reserved=true
end
seed(4000)
inventory[5].reserved=false -- baseline parity; saved-lineup protection tested below
local old=inst();scans,rebuilds=0,0
local t=os.clock();old:onClickBtnAuto();local oldTime=os.clock()-t
local oldCount,oldExp,oldScans,oldBuilds=old.currCnt,old.currExp,scans,rebuilds
local oldSelected=upvalues(originalAuto).select_suit
dofile('../client2/fixes/refine_ui.lua');dofile('../client2/fixes/refine_ui.lua')
local fast=inst();scans,rebuilds=0,0
t=os.clock();fast:onClickBtnAuto();local fastTime=os.clock()-t
assert(fast.currCnt==oldCount and fast.currExp==oldExp and fast.currCnt==40,string.format('selection result changed: %d/%d vs %d/%d',fast.currCnt,fast.currExp,oldCount,oldExp))
local selected=upvalues(originalAuto).select_suit
for k in pairs(oldSelected)do assert(selected[k],'material priority changed')end
assert(scans==0 and rebuilds==0 and oldScans==6 and oldBuilds==1,'full inventory/list work retained')
for i=1,4 do assert(not selected[i],'locked/rare/shoes/equipped item selected')end
assert(not inventory[5].__disable_multiteam,'temporary equipment flag leaked')
assert(fast.esrvList.childItems[6].imgsure.visible and fast.esrvList.childItems[6].ctlIcon.opacity==64,'visible selection not updated')
print(string.format('PASS: shipped auto selection, same cheapest 40, protected materials and UI marks; scans %d -> %d, list rebuild %d -> %d; Lua-only %.4fs -> %.4fs',oldScans,scans,oldBuilds,rebuilds,oldTime,fastTime))

-- Inventory changes are read afresh; no stale candidate cache after a purchase,
-- lock, consumption or saved-lineup change. Fragment stacks remain whole.
inventory[6].is_lock=1
inventory[5].reserved=true
irons[80001]=setmetatable({suit_type=80001,star=1,kind='weapon',exp_value=0.5},mt)
irons[80002]=setmetatable({suit_type=80002,star=1,kind='shoe',exp_value=0.1},mt)
fast:onClickBtnAuto();local state=upvalues(originalAuto)
assert(not state.select_suit[5] and not state.select_suit[6] and state.select_iron_suit[80001] and not state.select_iron_suit[80002],'stale lock, saved-lineup or fragment protection failed')
-- Original confirmation still submits exactly the selections and registers one
-- locked callback. Its original three refreshes must become one.
local refreshBefore=preview
fast:onClickBtnOK()
assert(#sends==1 and #callbacks==1 and sends[1].itemnum==90000 and #sends[1].suits==39 and #sends[1].suit_irons==1,'confirmation payload/lock changed')
local globalReg=signal.regUI
rebuilds=0;callbacks[1]({suits={{itemnum=90000,grade=1,exp=fast.currExp}}})
assert(rebuilds==1 and preview==refreshBefore+1,'confirmation did not coalesce to one page refresh')
assert(signal.regUI==globalReg and fast.currCnt==0 and count(upvalues(originalAuto).select_suit)==0,'global signal changed or selection not cleared')
assert(not fast.__local_refine_batch,'batch guard leaked')
print('PASS: original confirmation payload and locked response; three native page refreshes -> one, shared selection cleared, global signal unchanged')

-- Reaching the player grade boundary must still stop before overspending.
seed(60);local edge=inst();MyData.grade=1;edge.data.exp=99990
edge:onClickBtnAuto();assert(edge.currExp==6 and edge.currCnt==1,'player grade boundary changed')
seed(6);inventory[6].is_lock=1;local empty=inst();empty:onClickBtnAuto();assert(empty.currCnt==0,'protected-only bag not empty')
print('PASS: grade boundary, empty/protected bag, fresh inventory and fragments')
