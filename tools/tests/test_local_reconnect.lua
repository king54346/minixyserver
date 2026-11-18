local login, probe, net = {}, {}, {}
local timers, events, listeners, messages = {}, {}, {}, {}
local connects, packets, panels, disconnects, worlds = 0, 0, 0, 0, 0
local callbacks, valid = nil, true
_SDK, _NETEASESDK = true, true
MyData = {uid=42,newUserMode=true}
SIG = {JUST_LOCK="unlock",LOADING_TIMEOUT="timeout",LOGIN_DONE="done"}
signal = {}
function signal.dispatch(name)
  events[name]=(events[name] or 0)+1
  local f=listeners[name]; if f then f() end
end
local utils = {}
function utils.call_out(delay,fn) timers[#timers+1]={delay,fn} end
function utils.message_box(msg) messages[#messages+1]=msg end
function utils.setTime(t) assert(t==123) end
local function flushZero()
  local copy=timers;timers={}
  for _,t in ipairs(copy) do if t[1]==0 then t[2]() else timers[#timers+1]=t end end
end
function probe.open_panel(mode) assert(mode=="login");panels=panels+1 end
function probe.localise_reconnect() error("obsolete SDK route installed") end
function login.isLogining() return false end
function login.net_close() end
function login.S_SEND_SID(data) valid=true end
function login.S_LOGIN() worlds=worlds+1 end
function login.S_LOGIN_FAIL() error("stock SDK login failure path reached") end
function login.is_sid_login_valid() return valid end
function login.get_cur_uid() return 42 end
function login.get_cur_sid() return "token" end
function login.set_cur_uid(uid) assert(uid==42) end
function login.get_serv_ip() return "127.0.0.1" end
function login.get_serv_port() return 60010 end
function login.sid_login() packets=packets+1 end
function login.sid_connect_login() error("SDK recursion") end
function login.netease_connect_login() error("SDK auth frame") end
function net.connect_serv(host,port,ok,fail)
  assert(host=="127.0.0.1" and port==60010)
  connects=connects+1;callbacks={ok=ok,fail=fail}
  return {}
end
function net.disconnect() disconnects=disconnects+1;login.net_close() end
local throwSend
function net.send_prot()
  assert(_NETEASESDK==false and not login.is_sdk_sid_login_enable(),"transport consulted the SDK")
  if throwSend then error("expected transport error") end
  return "sent"
end
package.loaded.offline_probe=probe
package.loaded.login=login
package.loaded.net=net
package.loaded.utils=utils
package.loaded.struct={unpack=function(_,data)return data end}
local hook
function __onreq(name,fn) assert(name=="login");hook=fn;fn(login) end
dofile(arg[1] or "../client2/apk-src/assets/lib/reconnect_fixes.lua")
local patched=login.sid_connect_login
hook(login);probe.localise_reconnect()
assert(login.sid_connect_login==patched)
assert(net.send_prot(1,{})=="sent" and _NETEASESDK==true)
throwSend=true
assert(not pcall(net.send_prot,1,{}) and _NETEASESDK==true,"SDK flag not restored after failure")
throwSend=false
login.sid_connect_login(true)
local oldTimer=timers[#timers][2]
for i=1,20 do login.netease_connect_login();login.reloginSdk() end
assert(connects==1 and packets==0 and login.isLogining())
callbacks.ok();assert(packets==1)
login.S_LOGIN({uid=42,srv_time=123})
assert(not login.isLogining() and events.done==1 and worlds==0 and MyData.uid==42)
login.sid_connect_login(true)
oldTimer();assert(login.isLogining(),"stale timer killed a new reconnect")
callbacks.ok()
local action=0
listeners.done=function()action=action+1 end
listeners.timeout=function()listeners.done=nil end
login.S_LOGIN_FAIL(1);flushZero()
assert(panels==1 and disconnects==1 and not login.isLogining() and login.getNtSdkSid()==nil)
local before=connects
login.sid_connect_login();assert(connects==before and panels==2)
signal.dispatch("done");assert(action==0,"expired-session operation was replayed")
login.S_SEND_SID({uid=42,sid="new-token"})
login.sid_connect_login();callbacks.fail();flushZero()
assert(disconnects==2 and messages[#messages]=="系统维护中，请稍后重试。")
login.sid_connect_login();local timeout=timers[#timers][2];timeout();flushZero()
assert(not login.isLogining() and disconnects==3)
login.sid_connect_login();login.S_LOGIN({uid=99,srv_time=123});flushZero()
assert(MyData.uid==42 and panels==3)
login.S_SEND_SID({uid=42,sid="another-token"})
valid=false;before=connects
login.sid_connect_login();assert(connects==before and panels==4)
login.new_connect_login();login.C_NEW_LOGIN();assert(panels==6)
print("PASS: SDK bypass, coalescing, SID expiry, stale timer, timeout, UID isolation, pending-request cancellation")
