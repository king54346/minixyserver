local hooks = {}
__onreq = function(name, fn) hooks[name] = fn end
local C = { init = function(self)
  for _,k in ipairs({"esrvFriends", "esrvFriendApply"}) do
    self[k]:enableDynamicCreate(1, true)
  end
end }
local function scroll()
 return {enableDynamicCreate=function(self,n,smooth) self.dynamic=n~=0;self.smooth=smooth end,
 enableOptimaze=function(self,flag) self.optimaze=flag end,
 setData=function(self,data) self.rows={};for _,d in ipairs(data) do self.rows[#self.rows+1]={
 setVisible=function(s,v)s.visible=v end,setSize=function(s,w,h)s.w=w;s.h=h end} end end,
 getChildItems=function(s)return s.rows end,
 updateItemsPos=function(s)for i,r in ipairs(s.rows)do r.y=-(i-1)*(r.h+10)end end,
 scrollToTop=function(s)s.top=true end}
end
assert(loadfile('../client2/fixes/friend_list.lua'))()
hooks['view.FriendView']({CFriendMainView=C})
local init=C.init
hooks['view.FriendView']({CFriendMainView=C})
assert(init==C.init,'duplicate hook')
MyData={grade=47,vip=15}
local view={esrvFriends=scroll(),esrvFriendApply=scroll(),lblFriendLimit={setText=function(s,t)s.text=t end}}
C.init(view)
for _,k in ipairs({'esrvFriends','esrvFriendApply'}) do
 assert(not view[k].dynamic and not view[k].optimaze and not view[k].smooth)
end
local data={};for i=1,59 do data[i]={uid=i} end
view.esrvFriends:setData(data)
assert(#view.esrvFriends.rows==59 and view.esrvFriends.top)
for i,r in ipairs(view.esrvFriends.rows)do assert(r.visible and r.h==140 and r.y==-(i-1)*150)end
assert(view.lblFriendLimit.text=='好友上限 59 / 87')
view.esrvFriends:setData({});assert(view.lblFriendLimit.text=='好友上限 0 / 87')
print('PASS: friend list disables pooled and deferred rows, hook is idempotent')

local result = {resetUI = function(self)
  for _, buttons in ipairs({self.usedButton, self.newUsedButton}) do
    for _, button in ipairs(buttons) do button.visible = false end
  end
end}
local function pendingButton()
  return {
    visible = true,
    complete = function(self) self.visible = true end,
    stopAllActions = function(self) self.complete = nil end
  }
end
local confirm, other = pendingButton(), pendingButton()
local resultView = {usedButton = {confirm}, newUsedButton = {other}, btnClose = confirm}
hooks['view.RewardView']({CUtilsRewardView = result})
result.resetUI(resultView)
for _, button in ipairs({confirm, other}) do
  if button.complete then button:complete() end
  assert(not button.visible, 'stale reward animation showed a recycled button')
end
assert(resultView.btnClose == nil)
print('PASS: switching result panels cancels pending confirmation animations')
