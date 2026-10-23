-- 本服 hotfix：config.fillStarColor / fillChatStarColor 用 "#c%x" 拼颜色码，没补零：2 星色 0x04D20F 写成 5 位
-- "4d20f"、3 星色 0x7EFC 只有 4 位，客户端按 6 位解析，把名字开头的字吞进颜色码，显示出多余 / 缺失的字符
-- （如精修吸纳石确认框里的法术名）。改成 "#c%06x"。
if not CFG or rawget(CFG, "__local_star_color") then return end
CFG.__local_star_color = true
CFG.fillStarColor = function(txt, star)
  local tr = LOCAL_TXT or function(s) return s end
  return string.format("#c%06x%s", CFG.STAR_COLOR[star], tr(txt))
end
local oldChat = CFG.fillChatStarColor
CFG.fillChatStarColor = function(txt, star, darkMode)
  local tb = darkMode and CFG.CHAT_STAR_COLOR_DARK or CFG.CHAT_STAR_COLOR_LIGHT
  if not tb or not tb[star] then
    return oldChat(txt, star, darkMode)
  end
  return string.format("#c%06x%s", tb[star], txt)
end
