-- Run against the shipped bytecode, not a substitute signal implementation.
local opens, closes, now, callbacks = 0, 0, 0, 0
G2=true
SIG={JUST_LOCK='just-lock',LOADING_TIMEOUT='loading-timeout'}
utils={print=function() end,print_log=function() end,table_print=tostring,
 keys=function(t) local a={}; for k in pairs(t) do a[#a+1]=k end; return a end}
local loading={setData=function() end}
ui_mgr={open=function() opens=opens+1; return loading end,close=function() closes=closes+1 end}
__G__TRACKBACK__=function(err) error(err) end
dofile('../client2/apk-src/assets/lib/signal.lua')
dofile(arg[1] or '../client2/fixes/ui_waits.lua')
local function reply() callbacks=callbacks+1 end
local canceled=signal.regUI('canceled',reply,{lock=true})
assert(canceled)
signal.unreg(canceled)
now=61
local before=opens
signal.clearUISignals()
assert(opens==before,'60-second cleanup reopened a canceled request loading screen')
signal.dispatch('canceled')
signal.dispatch('legacy-canceled')
assert(callbacks==0,'canceled callback resurrected')
local live=signal.regUI('live',reply,{lock=true})
before=opens
signal.clearUISignals(); signal.clearUISignals()
assert(opens==before,'cleanup reopened loading for an existing request')
signal.dispatch('live')
assert(callbacks==1,'live reply lost or duplicated')
local owner=signal.regUI('owned',reply,{lock=true})
signal.clearUISignals()
signal.unreg(owner)
signal.dispatch('owned')
assert(callbacks==1,'view destruction no longer canceled its original callback ID')
local unrelated=0
signal.regUI('plain',function() unrelated=unrelated+1 end)
signal.clearUISignals(); signal.dispatch('plain')
assert(unrelated==0,'ordinary UI listeners were not cleaned')
local after=0
signal.regUI('reentrant',function()
 signal.regUI('next-reply',function() after=after+1 end,{lock=true})
end,{lock=true})
signal.dispatch('reentrant'); signal.clearUISignals(); signal.dispatch('next-reply')
assert(after==1,'new request from a reply callback was lost')
local regs=signal.reg
dofile(arg[1] or '../client2/fixes/ui_waits.lua')
assert(signal.reg==regs,'patch installed twice')
print('PASS: shipped signal bytecode; canceled requests, live replies, stable owner IDs, repeated cleanup, normal UI cleanup, reentrant replies, idempotence')
