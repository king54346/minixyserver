local hooks={}
function __onreq(n,f)hooks[n]=f end
dofile('../client2/fixes/ascension.lua')
logic={activity={ACT_FEISHENG=1,getActInfo=function()return {{grade=20}}end}}
package.loaded['logic.activity']=logic.activity
MyData={grade=50}
STR_FORMAT=string.format
datatable_mgr={getDataInfoByType=function(_,typ)return typ==13010 end}
pet_mgr={}
hooks['logic.pet_mgr'](pet_mgr)
assert(pet_mgr.canUpGrade({star=3,type=13010,soul_grade=0}))
assert(not pet_mgr.canUpGrade({star=5,type=13010,soul_grade=0}))
MyData.grade=1;assert(not pet_mgr.canUpGrade({star=3,type=13010}));MyData.grade=50
local C={updateRequest=function(self)self.has_pet_count=5 end}
hooks['view.PetUpGradeView']({CPetUpGradeNewNewView=C})
local confirmation,request,text
utils={message_box=function(msg)error(msg)end}
view={AskBoxView={show=function(msg,fn)confirmation=fn end}}
local v=setmetatable({data={type=13010,star=3,soul_grade=0},cnt=5,
  crblRequest1={setText=function(_,s)text=s end},upgradeSelect={force_gold=1}}, {__index=C})
function v:getSoulCost()return 333 end
function v:getSilverCost()return 249900 end
function v:isEnoughDan()return true end
function v:isEnoughDanExt()return true end
function v:getValidMatCnt()return 3 end
function v:getValidMatCntExt()return 0 end
function v:getExtMatCnt()return 0 end
function v:getMaterialItem(i)return {} end
function v:isValidMaterialItem(m)return true end
function v:getGoldCost()return 0 end
function v:upgradeRequest(n)request=n end
v:updateRequest();assert(text:find('333') and text:find('249900'))
v:onClickBtnUpGrade();assert(confirmation and request==nil)
confirmation();assert(request==1 and v.data.soul_grade==0)
-- The generic module hook must support Lua module() returning boolean,
-- without changing require's return value for existing callers.
__onreq=nil
dofile('../client2/fixes/bootstrap.lua')
package.loaded['logic.test_bool']=true
_G.test_bool={value=2}
local count=0
__onreq('logic.test_bool',function(m)assert(m==test_bool);count=count+1 end)
assert(count==1 and require('logic.test_bool')==true and count==1)
print('PASS: ascension retains costs, material/grade checks and confirmation without mutating realm')
