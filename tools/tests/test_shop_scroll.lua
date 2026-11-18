local hooks = {}
function __onreq(name, install) hooks[name] = install end
dofile("../client2/fixes/shop_scroll.lua")

scaleX, scaleY = 1, 2
local function exercise(module, name, field, samePage)
  local scroll = { y = -120, minHeight = 500 }
  function scroll:getContentOffset() return 0, self.y end
  function scroll:setContentOffset(_, y) self.y = y / scaleY end
  function scroll:getHeight() return 200 end
  function scroll:updateData(keep) assert(keep) end
  local C = {}
  function C:setData(data)
    self.data = data
    scroll.y = -300
    scroll.minHeight = data.height or 500
  end
  hooks[module]({ [name] = C })
  local view = setmetatable({ data = { libao_id = 1 } }, { __index = C })
  if field == "items" then view.ctlItems = { esrvContent = scroll }
  else view[field] = scroll end
  view:setData({ libao_id = 1 })
  assert(scroll.y == -120, name .. " lost scroll position")
  view:setData({ libao_id = 1, height = 250 })
  assert(scroll.y == -50, name .. " did not clamp shortened list")
  if samePage then
    view:setData({ libao_id = 2 })
    assert(scroll.y == -300, name .. " reused another category's offset")
  end
end
exercise("view.Mall_ItemView", "CMall_ItemView", "items", true)
exercise("view.Mall_LibaoView", "CMall_LibaoView", "esrvContent", true)
exercise("view.XianLingMainView", "CXianLingShopView", "esrvExchangeList")
print("PASS: shop refresh preserves scaled scroll offset and clamps shortened lists")
