do
-- 2026-11-16：销毁卡牌边框前加二次确认。
-- 服务端已改为一律返还 25 个同款碎片（50 个碎片合成一个）；客户端原本点「销毁」直接发协议、没有确认。
__onreq("view.DIYView", function(m)
  local C = (m and m.CDIYDetailView) or rawget(_G, "CDIYDetailView")
  if type(C) ~= "table" or C.__local_destroy_confirm then return end
  local destroy = C.onClickBtnDestroy
  if type(destroy) ~= "function" then return end
  C.__local_destroy_confirm = true
  C.onClickBtnDestroy = function(self, ...)
    local d = (self and self.netData) or {}
    local key = tonumber(d.key)
    local now = os.time()
    if key and self.__destroyKey == key and now - (self.__destroyTime or 0) <= 10 then
      self.__destroyKey, self.__destroyTime = nil, nil
      return destroy(self, ...)
    end
    self.__destroyKey, self.__destroyTime = key, now
    if utils and utils.message_box then
      utils.message_box("销毁该边框将返还 25 个同款碎片。\n再次点击「销毁」确认（10 秒内有效）。")
    end
  end
end)

end
