-- 本服 hotfix：阵法八卦图点击位置偏差（用户报：点鸟阵进了天阵、有的点了没反应）。原版 CZhenFaInfoView 判定点击时
-- 把格点 boxPointN 的局部坐标加上写死的 (15, 15 + 150) 当屏幕坐标，界面被适配挪动（长屏手机）后就对不上。
-- 这里改成用 convertToWorldSpace 把格点换算成真实屏幕坐标再判定（触摸 x, y 本来就是屏幕坐标）。

-- 与原版相同：第 i 块是格点 0、i、i+1 围成的三角形（第 8 块回到格点 1），对应的图和阵法号
local ctrlList = { "imgSnake", "imgSky", "imgWind", "imgDragon", "imgBird", "imgGround", "imgClound", "imgTiger" }
local IdxsMap = { 8, 1, 3, 5, 7, 2, 4, 6 }

local function inTri(ax, ay, bx, by, cx, cy, px, py)
  local function cross(x1, y1, x2, y2, x3, y3) return (x2 - x1) * (y3 - y1) - (y2 - y1) * (x3 - x1) end
  local d1, d2, d3 = cross(ax, ay, bx, by, px, py), cross(bx, by, cx, cy, px, py), cross(cx, cy, ax, ay, px, py)
  local neg = d1 < 0 or d2 < 0 or d3 < 0
  local pos = d1 > 0 or d2 > 0 or d3 > 0
  return not (neg and pos)
end

-- hit 返回屏幕坐标 (x, y) 落在第几块（1–8），没有返回 nil。
local function hit(self, x, y)
  local pts = {}
  for j = 0, 8 do
    local box = self["boxPoint" .. j]
    if not box then return nil end
    pts[j] = { box:convertToWorldSpace(15, 15) } -- 格点中心（原版是局部坐标 + 15）
  end
  for i = 1, 8 do
    local b, c = pts[i], pts[i % 8 + 1]
    if inTri(pts[0][1], pts[0][2], b[1], b[2], c[1], c[2], x, y) then
      return i
    end
  end
end

if not rawget(_G, "__onreq") then return end
__onreq("view.ZhenFaMainView", function(m)
local V = rawget(m, "CZhenFaInfoView")
if type(V) ~= "table" or rawget(V, "__local_hit_fix") then return end
V.__local_hit_fix = true
local oldInit = V.init
V.init = function(self, ...)
  oldInit(self, ...)
  local bg = self.caniBg
  if not bg then return end
  local okUI, ui = pcall(require, "ui")
  bg._onTouchBegan = function(inst, x, y)
    local i = hit(self, x, y)
    if i and self[ctrlList[i]] and self[ctrlList[i]]:isVisible() then
      self[ctrlList[i]]:setOpacity(200)
    end
    if okUI and ui and ui.CBase and ui.CBase._onTouchBegan then
      return ui.CBase._onTouchBegan(inst, x, y)
    end
  end
  bg.onClick = function(inst, x, y)
    for k = 1, 8 do
      if self[ctrlList[k]] then self[ctrlList[k]]:setOpacity(255) end
    end
    local i = hit(self, x, y)
    if i then
      self:onClickIcon(IdxsMap[i])
    end
  end
end
end)
