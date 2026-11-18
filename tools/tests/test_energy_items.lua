-- Exercise the shipped energy view, including the screenshot's small-buns-only case.
CObject = {new = function(self, t) return setmetatable(t or {}, {__index = self}) end}
game = {UI_MINOR_LAYER_6 = 6}
ui_mgr = {PR_2_UI = 2}
utils = {is_ios = function() return false end}
package.loaded.utils, package.loaded.ui, package.loaded.uicreator = utils, {}, {}
MyData = {tpoint = 30, max_tpoint = 120, hpoint = 0, max_hpoint = 40}
PT = {ETC_PTO = {C_BUY_POINT_CONFIRM = "eat", C_OPEN_POINT_BUY = "open"}}
local counts = {[10043] = 12, [10076] = 0, [10075] = 3}
local requests, used = {}, {}
item_mgr = {
  hasItem = function(id) return counts[id] or 0, id + 1 end,
  useItem = function(pos, _, _, amount) used[#used + 1] = pos; used.amount = amount end
}
net = {send_prot = function(spec, body) requests[#requests + 1] = {spec, body} end}
local function widget()
  return {
    setText = function(self, text) self.text = text end,
    setImage = function() end,
    setColor = function() end,
    setEnable = function(self, enabled) self.enabled = enabled end
  }
end
assert(loadfile("../client2/apk-src/assets/script/view/GetEnergyView.lua"))("view.GetEnergyView")
local C = package.loaded["view.GetEnergyView"].GetEnergyItemView
local row = setmetatable({afimgIcon = widget(), lblTitle = widget(), lblDesc = widget(),
                         crblTimes = widget(), btnBuy = widget()}, {__index = C})
row:setData({type = 1, idx = 3, times = 0, itemId = 10076})
assert(not row.btnBuy.enabled, "original screenshot regression was not reproduced")
dofile("../client2/fixes/bootstrap.lua")
dofile("../client2/fixes/energy_items.lua")
row:refreshWidgets()
assert(row.data.times == 12 and row.btnBuy.enabled and row.btnBuy.text == "一键吃满")
assert(row.lblDesc.text:find("包子12个", 1, true))
local wrapped = C.onClickBtnBuy
dofile("../client2/fixes/energy_items.lua")
assert(C.onClickBtnBuy == wrapped, "duplicate hook wrapped twice")
row:onClickBtnBuy()
assert(#requests == 2 and #used == 0)
assert(requests[1][1] == "eat" and requests[1][2].buy_type == 3 and requests[1][2].type == 1)
assert(requests[2][1] == "open")
counts[10076] = 5
row:refreshWidgets()
assert(row.data.times == 17)
counts[10076], counts[10043] = 0, 0
row:refreshWidgets()
assert(not row.btnBuy.enabled)
counts[10043], MyData.tpoint = 10, 120
row:refreshWidgets()
assert(not row.btnBuy.enabled, "full stamina should stay disabled")
MyData.tpoint = 30
row:setData({type = 2, idx = 3, times = 3, itemId = 10075})
requests = {}
row:onClickBtnBuy()
assert(used[1] == 10076 and #requests == 1 and requests[1][2].type == 2)
row:setData({type = 1, idx = 1, times = 10})
requests = {}
row:onClickBtnBuy()
assert(requests[1][1] == "eat" and requests[1][2].buy_type == 1)

-- The bag's bun shortcut must coexist with gem-bag and silver batch actions.
local bag = {refreshWidgets = function() end, onClickBtnUse10 = function() used.other = true end}
package.loaded["view.ItemListView"] = {CItemListItemView = bag}
require("view.ItemListView")
local b = setmetatable({data = {type = 10043}, btnUse = {
  isVisible = function() return true end, setPosition = function() end
}, btnUse10 = {setVisible = function() end, setText = function(self, s) self.text = s end}}, {__index = bag})
b:refreshWidgets()
assert(b.btnUse10.text == "吃到满")
requests = {}
b:onClickBtnUse10()
assert(#requests == 1 and requests[1][2].buy_type == 3)
b.data.type = 12345
b:onClickBtnUse10()
assert(used.other)
dofile("../client2/fixes/gem_bag_batch.lua")
dofile("../client2/fixes/silver_batch.lua")
for _, id in ipairs({10309, 10452}) do
  b.data = {type = id, pos = 42, amount = 37}
  b:onClickBtnUse10()
  assert(used[#used] == 42 and used.amount == 37, "gem-bag batch action lost")
end
local slider = {setData = function(self, data, _, done) self.data = data; done(3) end}
ui_mgr.open = function(name)
  assert(name == "view.UtilsSliderView.CUtilsSliderView")
  return slider
end
MyData.cash = 0
b.data = {type = 10093, pos = 43, amount = 25, name = "银锭"}
b:onClickBtnUse10()
assert(slider.data.max == 25 and used[#used] == 43 and used.amount == 3)
b.data = {type = 10076}
requests = {}
b:onClickBtnUse10()
assert(#requests == 1 and requests[1][2].buy_type == 3, "other batch wrappers broke buns")
print("PASS: shipped energy view reproduces missing small buns; both sizes, full/empty states, request payload, wine and bag shortcuts")
