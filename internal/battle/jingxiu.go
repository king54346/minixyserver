package battle

import "turnrpg/internal/gamedata"

// 法术书精修（客户端 view/MagicDetailView.lua getMagicJxDesc、logic/magic_mgr.lua jingxiu_str）：
// 法术发动后对每个目标附加法术表 jingxiu_buff 指的 buff。
//   - buff 名带数字（燃烧1 … 燃烧10）：按精修次数 n 取 jingxiu_buff+n-1 那一级，必定附加（说明「法术发动后对目标:…」）；
//   - 其余（僵化、麻痹、锢魂、凝滞）：n×10% 几率附加。
//
// buff 的效果见 magic_buff.go applyBuff。
func (b *battle) jingxiu(u, t *unit, c *castable) {
	base := c.JXBuff
	if c.jx <= 0 || base == 0 || !t.alive() {
		return
	}
	info := gamedata.Buffs[base]
	if info == nil {
		return
	}
	id := base
	if info.Upgrades() {
		id = base + int32(min(c.jx, 10)) - 1
		if x := gamedata.Buffs[id]; x != nil {
			info = x
		} else {
			id = base
		}
	} else if !b.chance(c.jx * 10) {
		return
	}
	b.applyBuff(u, t, id)
}
