-- Confirm the shipped client switches from yesterday's board to the selection
-- page when the server resets the daily from_type/to_type to zero.
for _,n in ipairs({'utils','ui','uicreator','auto.wish','auto.pet','view_utils','view.ChildrenDayMgr','view.Child23_Ghost'})do package.loaded[n]={}end
CObject={new=function(self,t)return setmetatable(t or {},{__index=self})end}
game={};local opened={}
ui_mgr={getInst=function()return nil end,close=function()end,
  open=function(tag)opened[#opened+1]=tag end}
assert(loadfile('../client2/apk-src/assets/script/view/YuanxuView.lua'))('view.YuanxuView')
view.YuanxuView.S_OPEN_WISH({from_type=25001,to_type=25002,current={{pos=0,type=1}},can_rechoose=0})
assert(opened[#opened]=='view.YuanxuView.CYuanxuPickSoulView')
view.YuanxuView.S_OPEN_WISH({from_type=0,to_type=0,current={},can_rechoose=0,free_left=1})
assert(opened[#opened]=='view.YuanxuView.CYuanxuSelPetView','new day retained yesterday board')
print('PASS: shipped Yuanxu response handler reopens target selection after daily reset')
