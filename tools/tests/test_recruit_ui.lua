-- Execute the shipped view bytecode with native widgets mocked.
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
utils={getTime=function()return 0 end};ui={};game={};CFG={}
LOCAL_TXT=function(s)return s end;STR_FORMAT=string.format;ui_mgr={}
item_mgr={hasItem=function()return 0 end};red_mgr={setRedPoint=function()end}
for _,n in ipairs({'utils','ui','uicreator','view_utils','logic.shop_mgr'})do package.loaded[n]={}end
package.loaded.utils=utils;package.loaded['logic.red_mgr']=red_mgr
package.loaded['view.AskBoxView']={};package.loaded.screenadapt_mgr={}
assert(loadfile('../client2/apk-src/assets/script/view/ShopZhaomuViewNew.lua'))('view.ShopZhaomuViewNew')
local C=view.ShopZhaomuViewNew.CShopMainViewNew
local function widget()
 return setmetatable({setVisible=function(s,v)s.visible=v end,setImage=function(s,v)
   local digit=v:match('txt_zhaomu_(%d+)%.png')
   assert(not digit or tonumber(digit)<=9,'nonexistent digit image: '..v);s.image=v
  end,getPositionY=function()return 10 end},{__index=function()return function()end end})
end
local function inst()
 return setmetatable({curData={time=1,is_give_gold=0,race_choose_today=1,race_rest_time_today=0},resetTimer=function()end,startChangeTimer=function()end},
 {__index=function(s,k)if C[k]then return C[k]end;local w=widget();rawset(s,k,w);return w end})
end
dofile('../client2/fixes/bootstrap.lua');dofile('../client2/fixes/recruit_ui.lua')
local wrapped=C.refreshDfzj
dofile('../client2/fixes/recruit_ui.lua');assert(wrapped==C.refreshDfzj,'compatibility patch wrapped twice')
for method,field in pairs({refreshDfzj='rate',refreshMrtj='daily_left_2_baodi'})do
 local v=inst()
 for _,n in ipairs({0,1,9,10,50,99,1,0})do
  v.curData[field]=n;v[method](v)
  assert(v.curData[field]==n,'protocol data mutated')
  assert(v.afimgCishu1.visible==(n>0) and v.afimgCishu2.visible==(n>=10),'stale digit visibility')
  if n>0 then assert(v.afimgCishu1.image==string.format('ui/zhaomu/txt_zhaomu_%d.png',n>=10 and math.floor(n/10)or n))end
  if n>=10 then assert(v.afimgCishu2.image==string.format('ui/zhaomu/txt_zhaomu_%d.png',n%10))end
 end
end
print('PASS: shipped recruit view, both pools, 0/1/9/10/50/99 pity digits, transitions, immutable protocol data, APK/hotfix idempotence')
