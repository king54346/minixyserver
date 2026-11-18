package.loaded.utils = {}
local hooks, timers, seq = {}, {}, 0
local scheduler = {}
function scheduler:scheduleScriptFunc(fn) seq=seq+1; timers[seq]=fn; return seq end
function scheduler:unscheduleScriptEntry(id) timers[id]=nil end
CCDirector={sharedDirector=function() return {getScheduler=function() return scheduler end} end}
function __onreq(name,fn) if name=='war.netwar' then hooks[#hooks+1]=fn end end
local C={}
function C:nextCmd()
 self.depth=(self.depth or 0)+1; self.maxDepth=math.max(self.maxDepth or 0,self.depth)
 self.count=(self.count or 0)+1
 if self.throw then self.depth=self.depth-1; error('test error') end
 if self.count==self.asyncAt then self.resume=function() self:nextCmd() end
 elseif self.count<self.limit then self:nextCmd() end
 self.depth=self.depth-1
end
function C:endWar() self.ended=true end
local m={CWarFastCmd=C}
if arg[2]=='baked-first' then
 dofile(arg[1]); for _,f in ipairs(hooks) do f(m) end; hooks={}
end
hooks={}; dofile(arg[1] or '../client2/fixes/battle_pipeline.lua'); for _,f in ipairs(hooks) do f(m) end
local wrapped=C.nextCmd; for _,f in ipairs(hooks) do f(m) end; assert(C.nextCmd==wrapped)
local function make(n,a) return setmetatable({limit=n,asyncAt=a},{__index=C}) end
local function tick() local copy={}; for k,v in pairs(timers) do copy[k]=v end; for _,f in pairs(copy) do f() end end
local function finish(s) local ticks=0; while next(timers) do tick(); ticks=ticks+1; assert(ticks<1000) end; return ticks end
local s=make(20000); s:nextCmd(); assert(s.count<=64,'UI thread not yielded'); local ticks=finish(s)
assert(s.count==20000 and s.maxDepth==1 and ticks>1)
local a=make(400,150); a:nextCmd(); finish(a); assert(a.count==150); a.resume(); finish(a); assert(a.count==400)
local e=make(20000); e:nextCmd(); local count=e.count; e:endWar(); finish(e); assert(e.count==count,'stale battle resumed')
local x=make(2); x.throw=true; assert(not pcall(function() x:nextCmd() end)); assert(not x.__local_frame_running and x.__local_frame_pending==0); x.throw=nil; x:nextCmd(); finish(x); assert(x.count==2)
print('PASS: 20000 commands in '..ticks..' frames; async wait, cancellation, errors, idempotence')
