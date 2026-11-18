-- Run the actual shipped friend-row renderer, including leader skills.
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
utils={isSpecialArea=function()return false end,isTaiWan=function()return false end,
 handleCustomIcon=function()end}
ui={};game={};ui_mgr={};CFG={};SIG={};PT={}
for _,n in ipairs({'utils','ui','uicreator','bit','logic.sdksocial_mgr'})do package.loaded[n]=n=='utils' and utils or {}end
LOCAL_TXT=function(s)return s end
STR_FORMAT=string.format
friend={isGM=function()return false end}
magic_mgr={getMagicInfoByType=function()return nil end}
datatable_mgr={getDataInfoByType=function(mod,id)assert(mod=='LeaderSkill');return {name='测试队长技能'}end}
assert(loadfile('../client2/apk-src/assets/script/view/FriendView.lua'))('view.FriendView')
local m=view.FriendView
local function widget()
 return setmetatable({setText=function(s,t)s.text=t end},{__index=function()return function()end end})
end
local function row(skill)
 return setmetatable({data={uid=20001,name='测试好友',grade=39,vip=15,fight_point=559509,
 leader_skl=skill,lastlogout=90000,is_online=0}}, {__index=function(s,k)
 if m.CFriendItemView[k] then return m.CFriendItemView[k] end
 local w=widget();rawset(s,k,w);return w end})
end
local ok,err=pcall(function()row(1):refreshWidgets()end)
assert(not ok,'expected to reproduce original leader-skill render failure')
print('Reproduced shipped renderer error: '..tostring(err))
dofile('../client2/fixes/bootstrap.lua')
dofile('../client2/fixes/friend_list.lua')
local r=row(1);r:refreshWidgets()
assert(r.lblSkill.text=='队长技能：测试队长技能')
row(0):refreshWidgets()
print('PASS: real friend renderer with leader skill no longer aborts list creation')
