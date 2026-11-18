local hooks = {}
function __onreq(name, fn) hooks[name] = fn end
dofile('../client2/fixes/card_frames.lua')
local received
local nw = {S_WAR_DATA=function(m) received=m return 17 end}
local C={reset=function(self,d) self.last=d end}
hooks['war.netwar'](nw)
hooks['view.WarObjView']({CWarObjView=C})
local msg={otherparam={{idx=12,value='1:2|102:7|103:999'}}}
assert(nw.S_WAR_DATA(msg)==17 and received==msg)
local view=setmetatable({},{__index=C})
view:reset({pos=102});assert(view.last.diyFrame==7)
view:reset({pos=1});assert(view.last.diyFrame==2)
view:reset({pos=103});assert(view.last.diyFrame==nil)
nw.S_WAR_DATA({otherparam={}})
view:reset({pos=102});assert(view.last.diyFrame==nil)
nw.S_WAR_DATA({otherparam='old replay'})
nw.S_WAR_DATA({otherparam={false,'bad',{idx=12,value=false}}})
print('PASS: frame metadata covers both sides, rejects unknown frames and resets between replays')
