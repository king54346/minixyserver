package xymsrv

import (
	"math/rand/v2"
	"slices"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 装备镶嵌与炼神（装备详情「镶嵌」「炼神」→ view/SuitDetailView.lua、view/LianShengMainView.lua）。
//
// 镶嵌（ITEM_PTO.C_ADD_PROP_STONE{itemnum, stone_type} / C_PULL_OUT_SUIT_STONE{itemnum} /
// C_PULL_OUT_ALL_SUIT_STONE{suits[itemnum]}，回 S_UPDATE_SUIT，装备的 stone 字段是宝石道具号）：
//   - logic/item_mgr.lua：武器镶太阳石（物理攻击）/ 红玛瑙（法术伤害），衣服镶光芒石（物理抗性）/ 月亮石（法术抗性），
//     鞋镶神秘石（被治疗效果）/ 黑玉石（治疗效果），各 +宝石等级%（getStoneDesc2）。
//   - 宝石描述：装备达到 20 级后可镶嵌。镶嵌规则（SuitDetailView stoneDesc）：只能换成相同或更高等级的宝石，原宝石返还；用凿晶石 10216 取出。
//   - 战斗：宝石加成按队长技同类加成相加（battle.Fighter.Gems）。
//
// 炼神（装备 skill 字段是神通号；S_REFRESH_SUIT_SKILL{cur_skill, save_skill[pos, skill]}）：
//   - 帮助页：装备 20 级开启，每 10 级多一批效果——20：诛仙 兽性 驱魔 斗兽 人杰 魔王 仙师 兽灵；
//     30：驭兵 逆雷 辟土 返火 分水 破妄；40：不侵 破岩 蹈火 驱雷 踏浪 灭法；50：闪避 突袭。
//   - 炼神 C_REFRESH_SUIT_SKILL 消耗 1 个炼神石 10140（先用每日免费次数，C_ON_QUEST_REFRESH_SUIT_SKILL_FREECNT →
//     S_QUEST_REFRESH_SUIT_SKILL_FREECNT{free_count}），回 S_UPDATE_SUIT（客户端等它）；
//     存为备用 C_SAVE_SUIT_SKILL 消耗凝神石 10342：20 / 30 / 40 / 50 级效果 1 / 2 / 6 / 10 个；
//     替换 C_REPLACE_SUIT_SKILL{itemnum, pos} 用备用的覆盖当前。
//   - 套装：同一护法穿的武器、衣服、鞋里有两件炼神效果相同，即获得该神通（suit_mgr.skill_str「套装效果(n/2)」）。
//   - 抓包：效果等级只见过 1 / 2 / 5 / 8；同名两族时用 21xxx 那组号（驭兵 21631、突袭 21621…）。
//
// 用户给定：炼神出的效果都是 1 级，套装效果的等级由宝石激活 = max(1, ⌊(武器 + 防具 + 鞋子宝石等级) / 3⌋)，
// 没镶嵌的按 0 级、短板拉低均值，最多 10 级（旧存档里的 2 级以上效果读档时归成 1 级）。
// 仍要两件相同才激活。例：武器突袭、防具突袭、鞋子仙师，三颗 5 级宝石 → 突袭 5 激活，仙师只有一件不激活。
// 备用最多 10 个（用户给定）。
// 估计：每天 3 次免费炼神。

const (
	lianshenStone = 10140
	ningshenStone = 10342
	zaojingStone  = 10216
	lsFreeDaily   = 3
	lsMaxSaved    = 10
	inlayGrade    = 20 // 宝石描述「武器达到20级后可镶嵌」
)

// lsFamilies：炼神效果族（1 级神通号）按开启等级。
var lsFamilies = []struct {
	grade int
	save  int32 // 存为备用的凝神石
	bases []int32
}{
	{20, 1, []int32{20071, 20081, 20091, 20101, 21221, 21241, 21201, 21261}},
	{30, 2, []int32{21631, 20391, 20411, 20421, 20401, 21711}},
	{40, 6, []int32{20031, 21591, 21601, 21571, 21581, 21701}},
	{50, 10, []int32{21611, 21621}},
}

// lsInfo 由炼神神通号找回所在族的 1 级号和存档消耗；不是炼神效果返回 0。
func lsInfo(skill int32) (base, save int32) {
	for _, f := range lsFamilies {
		for _, b := range f.bases {
			if skill >= b && skill < b+10 {
				return b, f.save
			}
		}
	}
	return 0, 0
}

// gemSlot：宝石类别（太阳石 1 … 黑玉石 6）对应的装备位（0 武器 1 衣服 2 鞋）与 battle.Fighter.Gems 下标。
func gemKind(item int) (kind, slot int, ok bool) {
	i := slices.Index(gemBase, item-item%10)
	if i < 0 || item%10 < 1 || item%10 > 9 {
		return 0, 0, false
	}
	return i, i / 2, true
}

// LianshenState 记每日免费炼神（Player.Lianshen）。
type LianshenState struct {
	Day  string `json:"day"`
	Used int    `json:"used"`
}

func (p *Player) lsFree() int {
	if p.Lianshen == nil || p.Lianshen.Day != today() {
		return lsFreeDaily
	}
	return max(0, lsFreeDaily-p.Lianshen.Used)
}

func init() {
	Handle("ITEM_PTO.C_ADD_PROP_STONE", onAddStone)
	Handle("ITEM_PTO.C_PULL_OUT_SUIT_STONE", func(s *Session, p *Player, m xym.Msg) {
		s.pullStones(p, []int32{int32(m["itemnum"].(int64))})
	})
	Handle("ITEM_PTO.C_PULL_OUT_ALL_SUIT_STONE", func(s *Session, p *Player, m xym.Msg) {
		var nums []int32
		l, _ := m["suits"].([]xym.Msg)
		for _, x := range l {
			nums = append(nums, int32(x["itemnum"].(int64)))
		}
		s.pullStones(p, nums)
	})
	Handle("ITEM_PTO.C_ON_QUEST_REFRESH_SUIT_SKILL_FREECNT", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("ITEM_PTO.S_QUEST_REFRESH_SUIT_SKILL_FREECNT", xym.Msg{"free_count": p.lsFree()})
	})
	Handle("ITEM_PTO.C_OPEN_SUIT_SKILL", func(s *Session, p *Player, m xym.Msg) {
		if su := p.suitByNum(int32(m["itemnum"].(int64))); su != nil {
			s.SendExt("ITEM_PTO.S_REFRESH_SUIT_SKILL", su.skillMsg())
		}
	})
	Handle("ITEM_PTO.C_REFRESH_SUIT_SKILL", onRefreshSuitSkill)
	Handle("ITEM_PTO.C_SAVE_SUIT_SKILL", onSaveSuitSkill)
	Handle("ITEM_PTO.C_REPLACE_SUIT_SKILL", onReplaceSuitSkill)
}

func (su *Suit) skillMsg() xym.Msg {
	l := []xym.Msg{}
	for i, sk := range su.Saved {
		l = append(l, xym.Msg{"pos": i + 1, "skill": sk})
	}
	return xym.Msg{"cur_skill": su.Skill, "save_skill": l}
}

func (s *Session) sendSuit(su *Suit) {
	p := s.player
	p.refreshShownSkills()
	s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": []xym.Msg{su.msg()}})
}

func onAddStone(s *Session, p *Player, m xym.Msg) {
	su := p.suitByNum(int32(m["itemnum"].(int64)))
	gem := int(m["stone_type"].(int64))
	_, slot, ok := gemKind(gem)
	switch {
	case su == nil || gamedata.Suits[su.Type] == nil:
		s.retMsg("装备不存在。")
		return
	case !ok || suitSlot[gamedata.Suits[su.Type].Kind] != slot:
		s.retMsg("这种宝石不能镶嵌在这个部位。")
		return
	case su.Grade < inlayGrade:
		s.retMsg("装备达到 20 级后才能镶嵌宝石。")
		return
	case p.itemCount(gem) < 1:
		s.retMsg("宝石不足。")
		return
	case su.Stone != 0 && gem%10 < su.Stone%10:
		s.retMsg("只能镶嵌与当前宝石相同或更高等级的宝石。")
		return
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(gem, 1)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	if su.Stone != 0 {
		d.items = append(d.items, p.addItem(su.Stone, 1)...)
	}
	su.Stone = gem
	p.saveLog(s)
	s.sendDelta(p, d)
	s.sendSuit(su)
}

// pullStones 用凿晶石取出装备上的宝石（每件一个凿晶石，不够就取到用完为止）。
func (s *Session) pullStones(p *Player, nums []int32) {
	d := &bagDelta{}
	var changed []xym.Msg
	for _, n := range nums {
		su := p.suitByNum(n)
		if su == nil || su.Stone == 0 {
			continue
		}
		if p.itemCount(zaojingStone) < 1 {
			s.retMsg("凿晶石不足。")
			break
		}
		ch, rm := p.takeItems(zaojingStone, 1)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
		d.items = append(d.items, p.addItem(su.Stone, 1)...)
		su.Stone = 0
		changed = append(changed, su.msg())
	}
	p.saveLog(s)
	s.sendDelta(p, d)
	if changed == nil {
		changed = []xym.Msg{}
	}
	s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": changed})
}

// ensureSkill：装备到 20 级时开启炼神效果（帮助页「装备升级到20后将开启炼神效果」），随机给一个；返回是否新给了。
// 套装效果那一栏（含「炼神」按钮）客户端只在装备有 skill 时才显示。
func (su *Suit) ensureSkill() bool {
	if su.Skill != 0 || su.Grade < lsFamilies[0].grade {
		return false
	}
	su.Skill = rollSuitSkill(su.Grade, rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(su.ItemNum))))
	return su.Skill != 0
}

// rollSuitSkill 按装备等级可开的效果族随机一个炼神效果。
func rollSuitSkill(grade int, rng *rand.Rand) int32 {
	var pool []int32
	for _, f := range lsFamilies {
		if grade >= f.grade {
			pool = append(pool, f.bases...)
		}
	}
	if len(pool) == 0 {
		return 0
	}
	return pool[rng.IntN(len(pool))] // 炼神效果一律 1 级，等级由宝石激活（setBoost）
}

// normalizeSkills 把旧存档里 2 级以上的炼神效果（含备用）归成 1 级，返回是否有改动。
func (su *Suit) normalizeSkills() bool {
	changed := false
	if base, _ := lsInfo(su.Skill); base != 0 && base != su.Skill {
		su.Skill, changed = base, true
	}
	for i, sk := range su.Saved {
		if base, _ := lsInfo(sk); base != 0 && base != sk {
			su.Saved[i], changed = base, true
		}
	}
	return changed
}

func onRefreshSuitSkill(s *Session, p *Player, m xym.Msg) {
	su := p.suitByNum(int32(m["itemnum"].(int64)))
	if su == nil || su.Grade < lsFamilies[0].grade {
		s.retMsg("装备升级到 20 级后才能炼神。")
		if su != nil {
			s.sendSuit(su)
		}
		return
	}
	d := &bagDelta{}
	if p.lsFree() > 0 {
		if p.Lianshen == nil || p.Lianshen.Day != today() {
			p.Lianshen = &LianshenState{Day: today()}
		}
		p.Lianshen.Used++
	} else {
		if p.itemCount(lianshenStone) < 1 {
			s.retMsg("炼神石不足。")
			s.sendSuit(su)
			return
		}
		ch, rm := p.takeItems(lianshenStone, 1)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	}
	su.Skill = rollSuitSkill(su.Grade, rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(su.ItemNum))))
	p.saveLog(s)
	s.sendDelta(p, d)
	s.sendSuit(su)
	s.SendExt("ITEM_PTO.S_REFRESH_SUIT_SKILL", su.skillMsg())
}

func onSaveSuitSkill(s *Session, p *Player, m xym.Msg) {
	su := p.suitByNum(int32(m["itemnum"].(int64)))
	if su == nil || su.Skill == 0 {
		s.retMsg("当前没有炼神效果。")
		return
	}
	_, cost := lsInfo(su.Skill)
	switch {
	case slices.Contains(su.Saved, su.Skill):
		s.retMsg("这个效果已经保存过了。")
		return
	case len(su.Saved) >= lsMaxSaved:
		s.retMsg("备用炼神列表已满。")
		return
	case p.itemCount(ningshenStone) < cost:
		s.retMsg("凝神石不足。")
		return
	}
	d := &bagDelta{}
	ch, rm := p.takeItems(ningshenStone, cost)
	d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	su.Saved = append(su.Saved, su.Skill)
	p.saveLog(s)
	s.sendDelta(p, d)
	s.SendExt("ITEM_PTO.S_REFRESH_SUIT_SKILL", su.skillMsg())
}

func onReplaceSuitSkill(s *Session, p *Player, m xym.Msg) {
	su := p.suitByNum(int32(m["itemnum"].(int64)))
	pos := int(m["pos"].(int64))
	if su == nil || pos < 1 || pos > len(su.Saved) {
		s.retMsg("备用效果不存在。")
		return
	}
	su.Skill = su.Saved[pos-1]
	p.saveLog(s)
	s.sendSuit(su)
	s.SendExt("ITEM_PTO.S_REFRESH_SUIT_SKILL", su.skillMsg())
}

// gearExtras 是出战位 fp 所穿装备带来的战斗加成：套装神通（两件相同炼神效果，等级由宝石定）与宝石加成。
func (p *Player) gearExtras(fp int) (tals []int32, gems [6]int) {
	sl := p.fightSlot(fp)
	if sl == nil {
		return nil, gems
	}
	count := map[int32]int{} // 各炼神效果穿了几件
	gemLv := 0
	for _, pos := range sl.Suit {
		su := p.suitByPos(pos)
		if su == nil {
			continue
		}
		if base, _ := lsInfo(su.Skill); base != 0 {
			count[base]++
		}
		if kind, _, ok := gemKind(su.Stone); ok {
			gems[kind] += su.Stone % 10
			gemLv += su.Stone % 10
		}
	}
	for sk, n := range count {
		if n < 2 {
			continue
		}
		if b := setBoost(sk, gemLv); b != 0 {
			tals = append(tals, b)
		}
	}
	slices.Sort(tals)
	return tals, gems
}

// setBoost 是激活的套装效果的神通号：炼神效果都是 1 级，套装等级由宝石决定 = max(1, ⌊三件宝石等级和/3⌋)，
// 最多 10 级（用户给定；没镶嵌的按 0 级）；不是炼神效果返回 0。
func setBoost(skill int32, gemLv int) int32 {
	base, _ := lsInfo(skill)
	if base == 0 {
		return 0
	}
	return base + int32(min(max(gemLv/3, 1), 10)) - 1
}

// refreshShownSkills 重算出战阵容里各装备显示的炼神效果（穿着的都显示宝石提升后的等级，没成套的客户端自己标「未激活」；客户端只认
// S_UPDATE_SUIT 的 skill），返回显示有变化的装备。存档里的 Skill 不变（炼神、备用都按它）。
func (p *Player) refreshShownSkills() []*Suit {
	want := map[*Suit]int32{}
	for fp := 1; fp <= lineupSlots; fp++ {
		sl := p.fightSlot(fp)
		if sl == nil {
			continue
		}
		var worn []*Suit
		gemLv := 0
		for _, pos := range sl.Suit {
			if su := p.suitByPos(pos); su != nil {
				worn = append(worn, su)
				if _, _, ok := gemKind(su.Stone); ok {
					gemLv += su.Stone % 10
				}
			}
		}
		for _, su := range worn {
			if base, _ := lsInfo(su.Skill); base != 0 {
				if b := setBoost(su.Skill, gemLv); b != su.Skill {
					want[su] = b
				}
			}
		}
	}
	var changed []*Suit
	for _, su := range p.Suits {
		if su.shown != want[su] {
			su.shown = want[su]
			changed = append(changed, su)
		}
	}
	return changed
}
