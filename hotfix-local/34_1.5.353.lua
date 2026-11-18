do
-- 2026-11-16：领土战场入口修正 + 诊断。
-- 1) 从洞府差事点「领土帮战 → 前往」时，洞府主界面还开着，地图会开在它下面（被挡住）。
--    这里在收到 S_TERRITORY_OPEN_MAIN 前后关掉洞府主界面，并把地图置顶。
-- 2) 仍保留 pcall，界面再出错会弹框显示错误文本。
__onreq("logic.dongfu_mgr", function(m)
  local function wrap(tbl)
    if type(tbl) ~= "table" then return end
    local orig = rawget(tbl, "S_TERRITORY_OPEN_MAIN")
    if type(orig) ~= "function" or rawget(tbl, "__local_terr_dbg") then return end
    rawset(tbl, "__local_terr_dbg", true)
    tbl.S_TERRITORY_OPEN_MAIN = function(msg, ...)
      if type(msg) == "table" then rawset(_G, "__local_terr_data", msg) end
      pcall(function()
        if ui_mgr then ui_mgr.close("view.PartyView.CPartyMainView") end
      end)
      local ok, err = pcall(orig, msg, ...)
      -- 线上版不弹框：错误文本留在 _G.__local_terr_err，供连 adb 时排查。
      if not ok then rawset(_G, "__local_terr_err", tostring(err)) end
      pcall(function()
        local inst = ui_mgr and ui_mgr.getInst("view.DongFuWarZoneMainView.CDongFuWarZoneMainView")
        if inst and inst.topmost then inst:topmost() end
      end)
      return ok
    end
  end
  wrap(m)
  wrap(rawget(_G, "dongfu_mgr"))
end)
-- 主界面/战场把 OPEN_MAIN 的数据再转一手时会丢掉 has_exchange_score，
-- 导致 Z 点「兑换胜利点」不出现。这里在 setData 前把原始字段补回去。
__onreq("view.DongFuWarZoneMainView", function(v)
  local function patch(C)
    if type(C) ~= "table" or type(C.setData) ~= "function" or rawget(C, "__local_terr_inj") then
      return
    end
    rawset(C, "__local_terr_inj", true)
    local orig = C.setData
    C.setData = function(self, data, ...)
      local raw = rawget(_G, "__local_terr_data")
      if type(data) == "table" and type(raw) == "table" then
        if raw.has_exchange_score ~= nil then data.has_exchange_score = raw.has_exchange_score end
        if raw.xianqi ~= nil then data.xianqi = raw.xianqi end
      end
      return orig(self, data, ...)
    end
  end
  if type(v) == "table" then
    patch(rawget(v, "CDongFuWarZoneMainView"))
    patch(rawget(v, "CStageView"))
  end
  patch(rawget(_G, "CDongFuWarZoneMainUIView"))
  patch(rawget(_G, "CDongFuWarZoneMainView"))
end)
-- 客户端算动作掩码时 hasExchange 总是丢，导致「兑换胜利点」不显示。
-- 这里直接在 reSetAction 之后把 EXCHANGEPOINT 位强行或进去，让按钮稳定出现（任意据点可用）。
__onreq("view.DongFuWarZoneMainView", function(v)
  local C = v and rawget(v, "CStageView")
  if type(C) ~= "table" or type(C.reSetAction) ~= "function" or rawget(C, "__local_terr_force") then
    return
  end
  rawset(C, "__local_terr_force", true)
  local orig = C.reSetAction
  C.reSetAction = function(self, stepIdx, ...)
    local r = orig(self, stepIdx, ...)
    pcall(function()
      local ctl = self and self.ctlAction
      local d = ctl and ctl.data
      local mgr = rawget(_G, "dongfu_mgr")
      local bitlib = rawget(_G, "bit")
      if type(d) ~= "table" or type(bitlib) ~= "table" or type(mgr) ~= "table" or not mgr.EXCHANGEPOINT then
        return
      end
      local mask = bitlib.bor(d.mask or 0, mgr.EXCHANGEPOINT)
      if mask == d.mask then
        return
      end
      local nd = {}
      for k, val in pairs(d) do
        nd[k] = val
      end
      nd.mask = mask
      ctl:setData(nd)
    end)
    return r
  end
end)
end
