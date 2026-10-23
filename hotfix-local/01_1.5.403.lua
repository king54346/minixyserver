-- 本服 hotfix：招募界面在开放的招募池少于 3 个（人物 10 级以前）时，滚动动画
-- （ShopZhaomuViewNew 的 scrollToIdxCenter → common_action.moveTo）算出空坐标报错，
-- 把 changeToIdx 里「选中当前池」的赋值一起打断，点招募只弹「购买失败：数据错误」。
-- 这里让动画失败只跳过动画：复位 moving，后面的选池照常执行。
pcall(require, "view.ShopZhaomuViewNew")
local V = _G.view and _G.view.ShopZhaomuViewNew
local C = type(V) == "table" and V.CShopMainViewNew
if type(C) ~= "table" or rawget(C, "__local_scroll_fix") then return end
C.__local_scroll_fix = true
local scroll = C.scrollToIdxCenter
C.scrollToIdxCenter = function(self, ...)
  local ok = pcall(scroll, self, ...)
  if not ok then
    self.moving = false
  end
end
print("[LOCAL HOTFIX] zhaomu scroll fix installed")
