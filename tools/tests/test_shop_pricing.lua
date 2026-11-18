-- Real shipped view bytecode + real require hooks, with only native widgets mocked.
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
utils={};ui={};game={};CFG={REWARD_TYPE_EQUIP=1,REWARD_TYPE_MAGIC=2,REWARD_TYPE_CARD=3}
LOCAL_TXT=function(s)return s end;STR_FORMAT=string.format
local asks,requests,closed=0,{},0
local ask={showByStyle=function()asks=asks+1 end}
for _,n in ipairs({'utils','ui','uicreator','view.ExchangeView','logic.map_mgr','view_utils'})do package.loaded[n]={}end
package.loaded['view.AskBoxNewStyleView']=ask
ui_mgr={close=function()closed=closed+1 end}
pet_mgr={getPetConfigInfo=function()return {star=5,name='test',shape=1}end}
magic_mgr={getMagicInfoByType=function(id)return {star=id,name='magic'}end}
datatable_mgr={getDataInfoListByMod=function()return {[3]={star=3,name='suit'},[4]={star=4,name='suit'}}end}
PT={ETC_PTO={C_XIANLING_SHOP_IGNORE_STUFF='ignore',C_XIANLING_SHOP_REFRESH_GOODS_LIST='xl',C_YUNYOU_SHOP_REFRESH_GOODS_LIST='yy',C_MYSTERY_SHOP_REFRESH_GOODS_LIST='mystery'}}
net={send_prot=function(p,d)requests[#requests+1]={p,d}end}
local function widget()
 return setmetatable({setText=function(s,t)s.text=t end,setVisible=function(s,v)s.visible=v end},{__index=function()return function()end end})
end
local function inst(C,data)
 return setmetatable({data=data,itype=1,viewName='云游商人',refreshListRect=function()end,setTitleLevel=function()end},
  {__index=function(s,k)if C[k]then return C[k]end;local w=widget();rawset(s,k,w);return w end})
end
assert(loadfile('../client2/apk-src/assets/script/view/XianLingMainView.lua'))('view.XianLingMainView')
local m=view.XianLingMainView
local material=inst(m.CXianLingGetBuyView,{ikind='suit',type=3,pos=2,desc='test'})
material:refreshWidgets();assert(material.goldSpend==50,'baseline material price changed')
local shop=inst(m.CXianLingShopView,{list={},title=1,contribute=0,recommend=1,left_refresh=0,refresh_gold=200})
shop:refreshWidgets();assert(not shop.lblGold.text:find('元宝'),'baseline refresh label changed')
-- The rollback APK must leave the native historical shop view unchanged.
local file=assert(io.open('../client2/apk-src/assets/lib/local_fixes.lua','rb'))
local generated=file:read('*a');file:close()
for _,mark in ipairs({'__shop_material_prices','__shop_gold_prices','__shop_token_prices'})do
 assert(not generated:find(mark,1,true),'retired shop override remains in APK: '..mark)
end
dofile('../client2/fixes/bootstrap.lua');dofile('../client2/fixes/shop_materials.lua')
for _,kind in ipairs({'suit','magic'})do
 for star,price in pairs({[3]=50,[4]=300})do
  material.data.ikind,material.data.type=kind,star
  material:refreshWidgets();assert(material.goldSpend==price,'historical material label changed')
 end
end
assert(material.lblNew10.visible==false, 'duplicate heading remains')
for _,name in ipairs({'imgGold','imgGoldCommon','imgGoldElite'}) do
 assert(material[name].visible==false, 'stray coin image remains')
end
assert(material.btnToGate.text~='' and material.btnChallengeElite.text~='', 'blank pending gate caption')
material.data.mapId,material.data.gateId=3,2
map_mgr={getCurrMapId=function()return 3 end}
material:refreshWidgets()
assert(material.lblMapName.text=='第3章 第2关')
assert(not material.btnToGate.text:find('查询'),'unanswered gate query remains')
assert(material.lblName.visible and material.lblXingji.visible)
material:onClickBtnToBuy();assert(asks==1,'historical material confirmation missing');asks=0
for _,C in ipairs({m.CXianLingShopView,m.CXianLingYunView})do
 local v=inst(C,{list={},title=1,contribute=0,recommend=1,left_refresh=0,refresh_gold=20})
 v:refreshWidgets()
 assert(v.lblGold.text:find('x1',1,true),'historical refresh quote conversion changed')
 v:onClickBtnRefresh();assert(asks==0 and #requests>0,'paid action was rewrapped')
 v.data.left_refresh=1;v:refreshWidgets();assert(v.lblGold.text=='','free refresh label')
 v:onClickBtnRefresh();assert(asks==1,'free refresh confirmation changed');asks=0
end
print('PASS: retired shop wrappers removed; user material quotes 50/300, native confirmations and historical refresh labels/actions preserved')
