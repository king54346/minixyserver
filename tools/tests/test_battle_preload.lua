local hooks, timers, seq = {}, {}, 0
local scheduler = {}
function scheduler:scheduleScriptFunc(fn, interval) seq=seq+1; timers[seq]={fn=fn,interval=interval}; return seq end
function scheduler:unscheduleScriptEntry(id) timers[id]=nil end
CCDirector={sharedDirector=function() return {getScheduler=function() return scheduler end} end}
function __onreq(name,fn) if name=='war.netwar' then hooks[#hooks+1]=fn end end
local queued, loads, mode = {}, 0, 'sync'
package.loaded.utils={loadImageToCache=function(res, async, cb)
 loads=loads+1
 if mode=='sync' then cb(); cb() elseif mode=='mixed' and loads%2==0 then cb() else queued[#queued+1]=cb end
end}
package.loaded['war.config']={powerFullEffect=1,powerEffect=2}
local C={nextCmd=function() end,endWar=function(self) self.strCmd=nil end}
local m={CWarFastCmd=C,CAction={},CAddPower={}}
local emptyNames={'CAddDingTouJian','CSwordAttack','CBloodPoolIn','CBloodPoolAtt','CAIDialog','CAdTempTalent','CDiTingCount','CXiuLuoCount'}
for _,name in ipairs(emptyNames) do m[name]={} end
dofile(arg[1] or '../client2/fixes/battle_pipeline.lua'); for _,f in ipairs(hooks) do f(m) end
local function flush()
 local copy=queued; queued={}
 for i=#copy,1,-1 do copy[i](); copy[i]() end
end
local called=0
local function done() called=called+1 end
m.preloadImage({},false,done); assert(called==1,'empty list stalled')
called=0; loads=0
m.preloadImage({'a','b','c'},false,function() assert(loads==3,'early completion'); done() end)
assert(called==1,'sync/duplicate callbacks')
called=0; mode='mixed'; loads=0
m.preloadImage({'a','b','c'},false,done); assert(called==0); flush(); assert(called==1)
called=0
m.CAction.preloadRes({cmdSequence={}},false,done); assert(called==1)
called=0
local command={preloadRes=function(self,sync,cb) m.preloadImage({'a','b'},sync,cb) end}
m.CAction.preloadRes({cmdSequence={command,command}},false,done); assert(called==0); flush(); assert(called==1,'nested command completion')
for _,name in ipairs(emptyNames) do called=0; m[name].preloadRes({},false,done); assert(called==1,name..' stalled') end
mode='sync'; called=0; loads=0
m.CAddPower.preloadRes({targets={{op=1}}},false,done); assert(called==1 and loads==2,'power callback missing')
called=0; m.CAddPower.preloadRes({targets={{op=0}}},false,done); assert(called==1)
m.CmdDict={[1]={create=function() return {
 parseData=function(self,str,index) return index end,
 preloadRes=function(self,sync,cb) m.preloadImage({'a','b'},sync,cb) end
} end}}
local function make() return setmetatable({strCmd=string.rep(string.char(1),100),curIndex=1,cmdSequence={}},{__index=C}) end
called=0; local s=make()
s:asyncLoadCmdRes(30,function() assert(#s.cmdSequence==30); done() end)
assert(called==1 and s.curIndex==31 and not next(timers),'sync callbacks exceeded batch size')
called=0; mode='async'; s=make(); s:asyncLoadCmdRes(10,done)
assert(called==0 and #s.cmdSequence==10 and next(timers)); flush(); assert(called==1 and not next(timers))
called=0; s=make(); s:asyncLoadCmdRes(10,done); s:endWar(); flush(); assert(called==0 and not next(timers),'stale battle entered')
called=0; s=make(); s:asyncLoadCmdRes(10,done)
for _,timer in pairs(timers) do assert(timer.interval==10); timer.fn() end
assert(called==1 and not next(timers),'missing asset did not recover'); flush(); assert(called==1,'late callback entered twice')
called=0; s=make(); s.curIndex=101; s:asyncLoadCmdRes(10,done); assert(called==1,'exhausted record stalled')
print('PASS: empty/sync/async/duplicate/nested preloads, all callback omissions, bounded batches, cancellation, timeout and late callback')
