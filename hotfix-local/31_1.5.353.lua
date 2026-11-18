do
-- Client UI hooks: reapply maintained fixes after view modules are reloaded.
if not rawget(_G, "__onreq") then
  local hooks = {}
  local req = require
  local function apply(entry, m)
    if entry.applied[m] then return end
    -- Mark before calling: patches may require this module (directly or via
    -- another module) while installing. Cached reads must not reinstall them.
    entry.applied[m] = true
    local ok, err = pcall(entry.fn, m)
    if not ok then
      entry.applied[m] = nil
      print("[CLIENT FIX] " .. entry.name .. ": " .. tostring(err))
    end
  end
  local function moduleTable(name, value)
    if type(value) == "table" then return value end
    local node = _G
    for part in string.gmatch(name, "[^%.]+") do
      node = type(node) == "table" and rawget(node, part) or nil
    end
    if type(node) == "table" then return node end
    node = rawget(_G, name:match("([^%.]+)$"))
    if type(node) == "table" then return node end
  end
  __onreq = function(name, fn)
    hooks[name] = hooks[name] or {}
    local entry = {name = name, fn = fn, applied = setmetatable({}, {__mode = "k"})}
    table.insert(hooks[name], entry) -- 同一模块可以挂多个补丁
    local m = moduleTable(name, package.loaded[name])
    if type(m) == "table" then apply(entry, m) end
  end
  require = function(name, ...)
    local loaded = package.loaded[name]
    local result = req(name, ...)
    local list = hooks[name]
    if not list then return result end
    local m = moduleTable(name, result)
    if type(m) == "table" then
      -- Invalidate the whole batch first: one hook may reenter require and
      -- install another hook before the outer loop reaches it.
      if not loaded then
        for _, entry in ipairs(list) do entry.applied[m] = nil end
      end
      for _, entry in ipairs(list) do
        apply(entry, m)
      end
    end
    return result
  end
end


-- Preserve equipment bag positions above 255 in the stock uint8 lineup fields.
-- The matching server uses a flag in the otherwise unused talent field.
local function decode(d)
  for _, l in ipairs(d.lineups or {}) do
    for _, r in ipairs(l.info or {}) do
      for i=1,3 do
        local k,t="spos"..i,"s_talent_"..i
        local h=r[t] or 0
        if h>=32768 and h<33024 then
          r[k]=(r[k] or 0)+(h-32768)*256
          r[t]=0
        end
      end
    end
  end
end
__onreq("logic.multiteam",function(m)
  if not m.__lp16 then
    m.__lp16=true
    for _, n in ipairs({"S_SEND_LINEUP","S_SEND_ONE_CLICK_EQUIP_LINEUP"}) do
      local f=m[n]
      m[n]=function(d) decode(d); return f(d) end
    end
  end
  if m.__lineupSuitKinds then return end
  m.__lineupSuitKinds = true
  local slotKinds = {"weapon", "shirt", "shoe"}
  m.getTeamPosSuits = function(no, fightPos)
    local data = {}
    local row = m.getTeamPosData(no, fightPos)
    if not row then return data end
    local kinds = {CFG.ITK_WEAPON, CFG.ITK_SHIRT, CFG.ITK_SHOE}
    for i, field in ipairs(slotKinds) do
      local suit = suit_mgr.getSuitByPos(row["spos"..i])
      if suit and suit.kind == kinds[i] then
        data[field] = suit.itemnum
      end
      local talent = "s_talent_"..i
      data[talent] = row[talent]
    end
    return data
  end
end)
__onreq("net",function(m)
  if m.__lp16 then return end
  m.__lp16=true
  local send=m.send_prot
  local function copy(t)
    if type(t)~="table" then return t end
    local r={}; for k,v in pairs(t) do r[k]=copy(v) end; return r
  end
  m.send_prot=function(p,d)
    local l=PT.LINEUP_PTO
    if p==l.C_SET_LINEUPS or p==l.C_CALC_FIGHTPOINT or p==l.C_ONE_CLICK_EQUIP then
      d=copy(d)
      for _, a in ipairs(d.lineups or {}) do
        for _, r in ipairs(a.info or {}) do
          for i=1,3 do
            local k="spos"..i
            local n=r[k] or 0
            r[k]=n%256
            r["s_talent_"..i]=32768+math.floor(n/256)
          end
        end
      end
    end
    return send(p,d)
  end
end)

end
