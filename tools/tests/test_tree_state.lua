package.loaded.utils={}
assert(loadfile('../client2/apk-src/assets/script/logic/tree_mgr.lua'))('logic.tree_mgr')
tree_mgr=logic.tree_mgr
local hooks={}
__onreq=function(n,f)hooks[n]=f end
local requests=0
tree_mgr.requestTreePlantInfo=function()requests=requests+1 end
local C={}
dofile('../client2/fixes/tree_state.lua')
hooks['logic.tree_mgr'](tree_mgr)
hooks['view.GoldTreeMainView']({CGoldTreeMainView=C})
local manager=tree_mgr
tree_mgr=nil -- Stock view uses a module-local manager, not a global alias.
local v=setmetatable({data={bug=2},setData=function(s,d)s.data=d end},{__index=C})
-- The stock manager receives data while the UI is hidden by the battle.
manager.S_OPEN_PLANT({bug=1})
assert(v.data.bug==2)
v:backshow();assert(v.data.bug==1 and requests==1)
manager.S_OPEN_PLANT({bug=0})
v:backshow();assert(v.data.bug==0 and requests==2)
print('PASS: tree state received during battle is applied on return and re-queried')
