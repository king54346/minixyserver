package xymsrv

import (
	"sort"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 天将（客户端也叫天匠、融合）：4、5 星装备 / 法术升阶，勇 武 尊 圣 神（lv 1–5）。
//
// 规则来源（client/lua/decompiled/script）：
//   - ui/Tianjiangzhuangbei_Help.lua、TianjiangFashu_Help.lua（说明文字，与用户给的规则一致）：阶级按**种类**记，
//     玩家所有该种装备 / 法术都吃加成；4 星只能用同名升阶；5 星同名每件 = 100 器魂（装备）/ 50 法魂（法术），
//     4 星同名每件 = 50；5 星整件分解 20 点、碎片 1 点；升阶后有冷却。
//   - 各阶累计（用户给出）：5 星装备 勇 1 件、武 1、尊 2、圣 3、神 5；4 星与 5 星法术 1、2、4、6、10。
//     换算成点数即下面 tjCum。抓包核对：4 星 0 阶 cost 50；5 星法术 2 阶 cost 100、4 阶 cost 200；
//     5 星装备 4 阶 cost 200（S_SEND_TIANJIANG_INFO 的 cost = 升下一阶还差的点数）。
//   - view/TianJiangDetailView.lua（非 trunk 分支，client2 走这条）：method 1 用器魂 / 法魂、2 用同名；
//     cost_type 1 付灵气、2 付元宝（= 灵气价 ×10）；5 星装备 0 阶用同名直接到「武」，灵气多付 60
//     （EQUIP_WU_LINGQI_COST）；同名件数 = max(1, cost/每件点数)。
//   - view/TianJiangMainView.lua：人物 <40 级最高「武」、<50 级最高「圣」；view/SuitDetailView.lua：30 级开放，
//     且金装（5 星）≥10 件（CardSetView.NEED_GOLD_SUIT_COUNT，法术同）。
//   - logic/suit_mgr.lua tianjian_prop_value（非 trunk）：装备加成 = floor(0.1×阶×该装备的加成值)，5 星另加先攻 +阶
//     （说明里只有战靴有先攻）；logic/magic_mgr.lua：法术触发几率 +2%×阶。
//
// 估计（待抓包校准）：升到 4 阶的灵气价 120（抓包有 20 / 90 / 150，60 来自客户端常量）、冷却时长、
// 灵气来源（洞府系统，本服还没有，灵气恒为 0，只能付元宝）、金装数量用「曾同时拥有的最多 5 星件数」近似
// （原服 gold_suit 是图鉴累计）。缘分 / 大缘的属性加成未实现（docs/tasks T6）。

const (
	tjTypeSuit  = 2 // CardSetView.TYPE_EQUIP
	tjTypeMagic = 3 // CardSetView.TYPE_MAGIC

	tjMethodSoul = 1
	tjMethodSame = 2
	tjCostLingqi = 1
	tjCostGold   = 2

	tjMaxLv        = 5
	tjOpenGrade    = 30 // 人物 30 级开放
	tjNeedGold     = 10 // 金装 / 金法术 10 件开放
	tjSoulPerWhole = 20 // 5 星整件分解得的器魂 / 法魂（碎片每个 1 点）
	tjCooldown     = 0  // 升阶冷却（秒）；原服有冷却但时长未知，待抓包
)

// tjLingqi[n] 是升到 n 阶的灵气价（元宝价 ×10）。
var tjLingqi = [tjMaxLv + 1]int32{0, 20, 60, 90, 120, 150}

// TianJiangState 是玩家的天将存档（Player.TianJiang）。
type TianJiangState struct {
	Suit   tjBook `json:"suit"`
	Magic  tjBook `json:"magic"`
	Lingqi int32  `json:"lingqi,omitempty"` // 灵气（洞府系统接入前恒为 0）
	// 金装 / 金法术件数的历史最高（原服 gold_suit / gold_magic 是图鉴累计，消耗后不减；
	// 本服没有获得记录，用「曾同时拥有的最多件数」近似）
	GoldSuit  int `json:"gold_suit,omitempty"`
	GoldMagic int `json:"gold_magic,omitempty"`
}

// tjBook 是装备或法术一类的天将数据。
type tjBook struct {
	Soul  int32            `json:"soul,omitempty"` // 器魂 / 法魂
	Types map[int]*tjEntry `json:"types,omitempty"`
}

// tjEntry 是一种装备 / 法术的天将进度。
type tjEntry struct {
	Lv        int   `json:"lv"`
	Points    int32 `json:"points"`               // 累计点数（同名件折算 + 器魂 / 法魂）
	CoolUntil int64 `json:"cool_until,omitempty"` // 冷却到期（Unix 秒）
}

func (p *Player) tj() *TianJiangState {
	if p.TianJiang == nil {
		p.TianJiang = &TianJiangState{}
	}
	return p.TianJiang
}

func (t *TianJiangState) book(kind int) *tjBook {
	if kind == tjTypeSuit {
		return &t.Suit
	}
	return &t.Magic
}

func (b *tjBook) entry(typ int) *tjEntry {
	if b.Types == nil {
		b.Types = map[int]*tjEntry{}
	}
	e := b.Types[typ]
	if e == nil {
		e = &tjEntry{}
		b.Types[typ] = e
	}
	return e
}

// suitTJ / magicTJ 是某种装备 / 法术的天将阶级（没升过为 0）。
func (p *Player) suitTJ(typ int) int {
	if p.TianJiang != nil && p.TianJiang.Suit.Types[typ] != nil {
		return p.TianJiang.Suit.Types[typ].Lv
	}
	return 0
}

func (p *Player) magicTJ(typ int32) int {
	if p.TianJiang != nil && p.TianJiang.Magic.Types[int(typ)] != nil {
		return p.TianJiang.Magic.Types[int(typ)].Lv
	}
	return 0
}

// tjStar 是 kind / typ 的星级；不是 4、5 星（不能升阶）时返回 0。
func tjStar(kind, typ int) int {
	st := 0
	switch kind {
	case tjTypeSuit:
		if si := gamedata.Suits[typ]; si != nil && si.Prop != "" {
			st = si.Star
		}
	case tjTypeMagic:
		if typ >= 11000 && typ < 15000 { // 法术书段（见 docs/DATA.md）
			if mi := gamedata.Magics[int32(typ)]; mi != nil {
				st = mi.Star
			}
		}
	}
	if st < 4 {
		return 0
	}
	return st
}

// tjCum 是升到各阶所需的累计点数；tjUnit 是一件同名折算的点数。
func tjCum(kind, star int) [tjMaxLv + 1]int32 {
	if kind == tjTypeSuit && star == 5 {
		return [tjMaxLv + 1]int32{0, 100, 100, 200, 300, 500}
	}
	return [tjMaxLv + 1]int32{0, 50, 100, 200, 300, 500}
}

func tjUnit(kind, star int) int32 {
	if kind == tjTypeSuit && star == 5 {
		return 100
	}
	return 50
}

// tjGradeCap 是人物等级允许的最高阶（TianJiangMainView：<40 级「武」、<50 级「圣」）。
func tjGradeCap(grade int) int {
	switch {
	case grade < tjOpenGrade:
		return 0
	case grade < 40:
		return 2
	case grade < 50:
		return 4
	}
	return tjMaxLv
}

// tjCost 是 e 从当前阶升下一阶还差的点数与灵气价；已满阶返回 0, 0。
func tjCost(kind, star int, e *tjEntry) (int32, int32) {
	if e.Lv >= tjMaxLv {
		return 0, 0
	}
	cum := tjCum(kind, star)
	return max(cum[e.Lv+1]-e.Points, 0), tjLingqi[e.Lv+1]
}

// tjLevelFor 是累计 points 点能到的最高阶（不超过 limit）。
func tjLevelFor(kind, star int, points int32, limit int) int {
	cum := tjCum(kind, star)
	lv := 0
	for l := 1; l <= limit; l++ {
		if points >= cum[l] {
			lv = l
		}
	}
	return lv
}

// goldCount 是金装 / 金法术件数（5 星，含同名）：当前拥有数与历史最高取大并记下。
func (p *Player) goldCount(kind int) int {
	n := p.goldOwned(kind)
	t := p.tj()
	if kind == tjTypeSuit {
		t.GoldSuit = max(t.GoldSuit, n)
		return t.GoldSuit
	}
	t.GoldMagic = max(t.GoldMagic, n)
	return t.GoldMagic
}

// goldOwned 是当前拥有的 5 星装备 / 法术书件数。
func (p *Player) goldOwned(kind int) int {
	n := 0
	if kind == tjTypeSuit {
		for _, s := range p.Suits {
			if si := gamedata.Suits[s.Type]; si != nil && si.Star == 5 {
				n++
			}
		}
		return n
	}
	for _, b := range p.Books {
		if mi := gamedata.Magics[b.Type]; mi != nil && mi.Star == 5 {
			n++
		}
	}
	return n
}

func (p *Player) goldPets() int {
	n := 0
	for _, pet := range p.Pets {
		if pi := gamedata.Pets[pet.Type]; pi != nil && pi.Star == 5 {
			n += 1 + pet.TLAdd // 含同名卡（化形开启提示「含同名卡」；本服同名卡并入 TLAdd）
		}
	}
	return n
}

// tjTypes 是天将列表里要列出的 4、5 星种类：当前拥有的，加上升过阶的。
func (p *Player) tjTypes(kind int) []int {
	set := map[int]bool{}
	if kind == tjTypeSuit {
		for _, s := range p.Suits {
			set[s.Type] = true
		}
	} else {
		for _, b := range p.Books {
			set[int(b.Type)] = true
		}
	}
	if p.TianJiang != nil {
		for t := range p.TianJiang.book(kind).Types {
			set[t] = true
		}
	}
	var out []int
	for t := range set {
		if tjStar(kind, t) > 0 {
			out = append(out, t)
		}
	}
	sort.Ints(out)
	return out
}

// tjInfoMsg 是 S_SEND_TIANJIANG_INFO：每种的阶级、冷却剩余秒数、下一阶的点数 / 灵气 / 元宝 / 银两价
// （满阶全 0，抓包如此）；gold 为角色元宝。
func (p *Player) tjInfoMsg(kind int, role *Role, now time.Time) xym.Msg {
	t := p.tj()
	bk := t.book(kind)
	list := []xym.Msg{}
	for _, typ := range p.tjTypes(kind) {
		e := &tjEntry{}
		if x := bk.Types[typ]; x != nil {
			e = x
		}
		cost, lingqi := tjCost(kind, tjStar(kind, typ), e)
		left := max(e.CoolUntil-now.Unix(), 0)
		list = append(list, xym.Msg{"itype": typ, "left_time": left, "cost": cost, "lingqi_cost": lingqi,
			"gold_cost": lingqi * 10, "money_cost": 0, "lv": e.Lv})
	}
	return xym.Msg{"type": kind, "soul": bk.Soul, "lingqi": t.Lingqi, "gold": role.Gold, "list": list}
}

// cardSetMsg 是 S_OPEN_CARD_SET（图鉴）：本服没有「曾经获得」的记录，按当前拥有列。
// state 2 = 已拥有，5 星为 4（抓包如此）；装备 / 法术的 count 为天将阶级，卡牌的 count 为拥有张数。
func (p *Player) cardSetMsg(kind int) xym.Msg {
	list := []xym.Msg{}
	gold := 0
	switch kind {
	case tjTypeSuit, tjTypeMagic:
		set := map[int]int{} // 种类 -> 星级
		if kind == tjTypeSuit {
			for _, s := range p.Suits {
				if si := gamedata.Suits[s.Type]; si != nil {
					set[s.Type] = si.Star
				}
			}
		} else {
			for _, b := range p.Books {
				if mi := gamedata.Magics[b.Type]; mi != nil {
					set[int(b.Type)] = mi.Star
				}
			}
		}
		for _, t := range p.tjTypes(kind) {
			set[t] = tjStar(kind, t)
		}
		types := make([]int, 0, len(set))
		for t := range set {
			types = append(types, t)
		}
		sort.Ints(types)
		for _, t := range types {
			state, lv := 2, 0
			if set[t] == 5 {
				state = 4
			}
			if kind == tjTypeSuit {
				lv = p.suitTJ(t)
			} else {
				lv = p.magicTJ(int32(t))
			}
			list = append(list, xym.Msg{"type": t, "state": state, "count": lv})
		}
		gold = p.goldCount(kind)
	default: // 卡牌
		count := map[int32]int{}
		for _, pet := range p.Pets {
			count[pet.Type]++
		}
		types := make([]int, 0, len(count))
		for t := range count {
			types = append(types, int(t))
		}
		sort.Ints(types)
		for _, t := range types {
			list = append(list, xym.Msg{"type": t, "state": 2, "count": count[int32(t)]})
		}
		gold = p.goldPets()
	}
	return xym.Msg{"type": kind, "gold_card_count": gold, "list": list}
}

// goldAttrMsg 是登录 / 图鉴时一起发的 S_ATTR_UPDATE（gold_card / gold_suit / gold_magic，客户端据此开放天将）。
func (p *Player) goldAttrMsg() xym.Msg {
	return xym.Msg{"attr": []xym.Msg{{"name": "gold_card", "val": p.goldPets()},
		{"name": "gold_suit", "val": p.goldCount(tjTypeSuit)}, {"name": "gold_magic", "val": p.goldCount(tjTypeMagic)}}}
}

// tjBonus 是一件装备的天将额外加成：floor(0.1×阶×加成值)。
func (p *Player) tjBonus(typ int, bonus int32) int32 {
	return bonus * int32(p.suitTJ(typ)) / 10
}

// tjSpeed 是出战位 fp 穿的 5 星战靴带来的先攻（+阶）。
func (p *Player) tjSpeed(fp int) int {
	sl := p.fightSlot(fp)
	if sl == nil {
		return 0
	}
	n := 0
	for _, pos := range sl.Suit {
		if s := p.suitByPos(pos); s != nil {
			if si := gamedata.Suits[s.Type]; si != nil && si.Star == 5 && si.Kind == "ITK_SHOE" {
				n += p.suitTJ(s.Type)
			}
		}
	}
	return n
}

// wornBookRates 与 wornBooks 按下标对应：每本书的天将几率加成（2%×阶）。
func (p *Player) wornBookRates(fp int) []int {
	var out []int
	if sl := p.fightSlot(fp); sl != nil {
		for _, pos := range sl.Book {
			if b := p.bookByPos(pos); b != nil {
				out = append(out, 2*p.magicTJ(b.Type))
			}
		}
	}
	return out
}

// 神通激发：5 星卡在人物 ≥30 级时，第 1 神通由所穿有缘 5 星法术书的天将阶激发（需金法术 ≥10），
// 第 2 / 3 神通由有缘 5 星衣服 / 武器的天将阶激发（需金装 ≥10）。等级 0–5，即 S_UPDATE_PET 的 tlxlv1–3，
// 战斗效果见 battle/jifa.go。
// 依据：client logic/magic_mgr.lua、suit_mgr.lua 的 tiangjiang_str（tl_idx 1 法术、2 衣服、3 武器，要求有缘且 5 星），
// view/PetNewDetailView.lua setTj（5 星、30 级、金法术 / 金装 ≥10）；官方抓包 7 张出战卡的 tlxlv 与对应装备的
// 天将阶逐一相符（东华帝君 tlxlv1 = 2 = 所穿 14505 的天将阶，其余 5/5/5）。
func (p *Player) petTJ(pet *Pet) [3]int {
	var out [3]int
	pi := gamedata.Pets[pet.Type]
	if pi == nil || pi.Star != 5 || p.grade() < tjOpenGrade {
		return out
	}
	sl := p.fightSlot(p.fightPos(pet.BagPos))
	if sl == nil {
		return out
	}
	if p.goldCount(tjTypeMagic) >= tjNeedGold {
		for _, pos := range sl.Book {
			if b := p.bookByPos(pos); b != nil && tjStar(tjTypeMagic, int(b.Type)) == 5 && pi.RelatedMagic(b.Type) {
				out[0] = max(out[0], p.magicTJ(b.Type))
			}
		}
	}
	if p.goldCount(tjTypeSuit) >= tjNeedGold {
		for _, pos := range sl.Suit {
			s := p.suitByPos(pos)
			if s == nil {
				continue
			}
			si := gamedata.Suits[s.Type]
			if si == nil || si.Star != 5 || !pi.RelatedEquip(int32(s.Type)) {
				continue
			}
			switch si.Kind {
			case "ITK_SHIRT":
				out[1] = max(out[1], p.suitTJ(s.Type))
			case "ITK_WEAPON":
				out[2] = max(out[2], p.suitTJ(s.Type))
			}
		}
	}
	return out
}
