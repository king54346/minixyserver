-- Exercise shipped view bytecode with lightweight widget/native dependencies.
local function widget()
  return setmetatable({setText=function(s,t)s.text=t end,getString=function(s)return s.text or '' end,
    getText=function(s)return s.text or '' end},{__index=function()return function()end end})
end
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
game={};ui_mgr={};CFG={STAR_COLOR={[1]=1},ITK_SHOE='shoe',isDisableWord=function()return false end}
LOCAL_TXT=function(s)return s end; STR_FORMAT=string.format
local messages,events,closed={},0,0
utils={is_android=function()return true end,message_box=function(s)messages[#messages+1]=s end,
 count=function(t)local n=0;for _ in pairs(t)do n=n+1 end;return n end,
 string_split_str=function(s)local a={};for v in string.gmatch(s,'[^,]+')do a[#a+1]=v end;return a end}
package.loaded.utils=utils;package.loaded.ui={};package.loaded.uicreator={}
package.loaded['logic.capacity']=true;package.loaded['auto.buff']=true
pet_mgr={analyzePetTalentInfo=function()return {} end}
local buffs={[5024]={name='燃烧4',desc='每回合损失$a%',param='8'},
 [5025]={name='燃烧5',desc='每回合损失$a%',param='10'},
 [5035]={name='毒砂5',desc='攻击下降$a%',param='10'}}
datatable_mgr={getDataInfoByType=function(_,id)return buffs[id]end,
 getDataInfoListByMod=function()return buffs end}
assert(loadfile('../client2/apk-src/assets/script/logic/buff_mgr.lua'))()
local asks=0
local ask={show=function()asks=asks+1 end}
package.loaded['view.AskBoxNewStyleView']=ask
local hooks={}
function __onreq(n,f)
  hooks[n]=hooks[n] or {};table.insert(hooks[n],f)
  if type(package.loaded[n])=='table' then f(package.loaded[n]) end
end
local function loadView(n)
  local base=arg[2]=='original' and '../client/jadx-out/resources/assets/script/' or '../client2/apk-src/assets/script/'
  assert(loadfile(base..n:gsub('%.','/')..'.lua'))(n)
  for _,f in ipairs(hooks[n] or {})do f(package.loaded[n])end
  return package.loaded[n]
end
if arg[1]~='direct' then
  dofile(arg[1] or '../client2/fixes/gameplay_ui.lua')
  dofile(arg[1] or '../client2/fixes/gameplay_ui.lua')
end
local function viewInst(C)
  return setmetatable({}, {__index=function(t,k)
    if C[k]~=nil then return C[k]end
    local w=widget();rawset(t,k,w);return w
  end})
end
-- Use the actual upgrade item click handler, including the shoe-only branch.
signal={dispatch=function()events=events+1 end}
ui_mgr.getInst=function()return nil end
local upgrade=loadView('view.SuitUpgradeView').CSuitUpgradeItemView
local item=setmetatable({data={type=1001,itemnum=2001,kind='shoe',is_lock=0}}, {__index=upgrade})
item:onClick()
assert(asks==0 and events==1,'rare-boots prompt blocked selection')
item:onClick();assert(events==2,'deselection broken')
ask.show('普通确认',function()error('must not auto-confirm unrelated dialogs')end)
assert(asks==1,'unrelated confirmation was bypassed')
-- Use the actual rename class with an unavailable SDK moderation endpoint.
CCommon={reviewWords=function()error('retired SDK called')end};MyData={grade=50}
ui_mgr.close=function()closed=closed+1 end
local rename=loadView('view.ChangeNameMainView').CChangeNameMainView
local r=viewInst(rename);r.crblwjmz.text='新名字';local submitted
r.cb=function(s)submitted=s end;r:onClickBtnOk()
assert(submitted=='新名字' and closed==1,'rename did not reach callback')
r.crblwjmz.text='bad name';r.cb=function()error('invalid name submitted')end;r:onClickBtnOk()
assert(#messages==1 and closed==1,'invalid name was not rejected')
print('PASS: shipped upgrade/rename views; shoe selection, ordinary confirmation, local rename validation')
