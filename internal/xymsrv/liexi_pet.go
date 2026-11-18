package xymsrv

import (
	"slices"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 时空裂隙里的护法：等级补到 人物等级×2，套装 / 宝石 / 精修无效，缘全部点亮，拥有的缘装备（武器 / 衣服）和缘法术书
// 自动装备（取等级最高的一件，天将阶有效），再加上装备的自选神通。客户端 S_*_UPDATE_TEAM.details 的字段
// （logic/temporal_rift_mgr.lua getPetDetailData）：rm* 缘法术、rw* 缘武器、rs* 缘衣服，armed_tal 自选神通，
// orgin_tal 自身神通，relate 缘（全亮）。

// lxGear 是护法在裂隙里自动装备的缘装备 / 缘法术书。
type lxGear struct {
	magic, weapon, shirt       int32 // 类型（0 = 没有）
	magicLv, weaponLv, shirtLv int
	magicTJ, weaponTJ, shirtTJ int
}

func (p *Player) lxGearOf(pi *gamedata.PetInfo) lxGear {
	var g lxGear
	for _, id := range pi.Relate {
		r := gamedata.Relates[id]
		if r == nil {
			continue
		}
		for _, m := range r.NMagic {
			for _, b := range p.Books {
				if b.Type == m && (g.magic == 0 || b.Grade > g.magicLv) {
					g.magic, g.magicLv, g.magicTJ = b.Type, b.Grade, p.magicTJ(b.Type)
				}
			}
		}
		for _, e := range r.NEquip {
			si := gamedata.Suits[int(e)]
			if si == nil {
				continue
			}
			for _, s := range p.Suits {
				if int32(s.Type) != e {
					continue
				}
				switch si.Kind {
				case "ITK_WEAPON":
					if g.weapon == 0 || s.Grade > g.weaponLv {
						g.weapon, g.weaponLv, g.weaponTJ = e, s.Grade, p.suitTJ(s.Type)
					}
				case "ITK_SHIRT":
					if g.shirt == 0 || s.Grade > g.shirtLv {
						g.shirt, g.shirtLv, g.shirtTJ = e, s.Grade, p.suitTJ(s.Type)
					}
				}
			}
		}
	}
	return g
}

// lxRelateRatio：缘全部点亮时的倍率（人物缘按满档，不超过 15%；已同修的取同修档）。
func lxRelateRatio(pet *Pet, pi *gamedata.PetInfo, hp bool) float64 {
	ratio := 1.0
	for _, id := range pi.Relate {
		r := gamedata.Relates[id]
		if r == nil {
			continue
		}
		vals := r.Att
		if hp {
			vals = r.HP
		}
		if len(r.NPet) > 0 && pi.Star >= 5 {
			if slices.Contains(pet.Together, id) && len(r.TogetherAtt) > 0 {
				vals = r.TogetherAtt
				if hp {
					vals = r.TogetherHP
				}
			}
			if len(vals) > 0 {
				ratio *= 1 + min(vals[0], 15)/100
			}
			continue
		}
		if len(vals) > 0 {
			ratio *= 1 + vals[0]/100
		}
	}
	return ratio
}

// lxGradeOf 是裂隙里的护法等级。
func (p *Player) lxGradeOf(pet *Pet) int {
	return min(max(pet.Grade, p.grade()*2), gamedata.MaxPetGrade)
}

// lxPet 是护法在裂隙里的属性与激发（tjPlus：本周增益兑换的「天匠激发 +N」）。
type lxPet struct {
	pet     *Pet
	pi      *gamedata.PetInfo
	grade   int
	att, hp int32
	speed   int
	gear    lxGear
	tals    []int32 // 自身神通（含第四神通）
	tj      [3]int
}

func (p *Player) lxPetOf(pet *Pet, tjPlus int) *lxPet {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return nil
	}
	x := &lxPet{pet: pet, pi: pi, grade: p.lxGradeOf(pet), gear: p.lxGearOf(pi), speed: pi.Speed}
	att := pi.Att(x.grade) + int32(pet.AttAdd)
	hp := pi.HP(x.grade) + int32(pet.HPAdd*pointHPStep)
	add := func(typ int32, lv int) {
		if si := gamedata.Suits[int(typ)]; si != nil && typ != 0 {
			b := si.Bonus(lv)
			b += p.tjBonus(int(typ), b)
			if si.Prop == "att" {
				att += b
			} else if si.Prop == "hp" {
				hp += b
			}
		}
	}
	add(x.gear.weapon, x.gear.weaponLv)
	add(x.gear.shirt, x.gear.shirtLv)
	x.att = int32(float64(att) * lxRelateRatio(pet, pi, false) * p.extendRatio(pet, false))
	x.hp = int32(float64(hp) * lxRelateRatio(pet, pi, true) * p.extendRatio(pet, true))
	x.tals, _ = p.activeTalents(pet, pi)
	if pi.Star == 5 && p.grade() >= tjOpenGrade {
		if tjStar(tjTypeMagic, int(x.gear.magic)) == 5 {
			x.tj[0] = x.gear.magicTJ
		}
		if si := gamedata.Suits[int(x.gear.shirt)]; si != nil && si.Star == 5 {
			x.tj[1] = x.gear.shirtTJ
		}
		if si := gamedata.Suits[int(x.gear.weapon)]; si != nil && si.Star == 5 {
			x.tj[2] = x.gear.weaponTJ
		}
		for i := range x.tj {
			x.tj[i] = min(x.tj[i]+tjPlus, 5)
		}
	}
	return x
}

// fighter 是裂隙里的战斗单位（站位 pos，装备 armed 这几个自选神通）。
func (x *lxPet) fighter(pos int, armed []int32) *battle.Fighter {
	tals := slices.Clone(x.tals)
	tj := make([]int, 0, len(tals))
	for i := range tals {
		if i < 3 {
			tj = append(tj, x.tj[i])
		} else {
			tj = append(tj, 0)
		}
	}
	for _, t := range armed {
		if t != 0 {
			tals = append(tals, t)
			tj = append(tj, 0)
		}
	}
	f := &battle.Fighter{ID: x.pet.NpcNum, Pos: pos, Name: x.pi.Name, Att: x.att, HP: x.hp, MaxHP: x.hp, Speed: x.speed,
		Icon: x.pet.Type, Race: x.pi.Race, Grade: x.grade, Talents: tals, TalentTJ: tj, Magic: x.pi.Magic,
		MagicLv: x.pet.magicLv(), SkillFull: x.pet.talentsFull(x.pi)}
	if x.gear.magic != 0 {
		f.Books = [][2]int32{{x.gear.magic, int32(x.gear.magicLv)}}
		f.BookRate = []int{2 * x.gear.magicTJ}
		f.BookTJ = []int{x.gear.magicTJ}
	}
	return f
}

// lxDetail 是 S_*_UPDATE_TEAM.details 的一项（存成结构体：竞技场对手 / 排行榜的阵容要存档）。
type lxDetail struct {
	PetType  int32   `json:"pet_type"`
	Pos      int     `json:"pos"`
	Grade    int     `json:"grade"`
	At       int32   `json:"at,omitempty"`
	HP       int32   `json:"hp,omitempty"`
	Sp       int     `json:"sp,omitempty"`
	TfmID    int32   `json:"tfmid,omitempty"`
	TfmLv    int     `json:"tfmlv,omitempty"`
	Gear     lxGearJ `json:"gear"`
	Armed    []int32 `json:"armed,omitempty"`
	Orgin    []int32 `json:"orgin,omitempty"`
	Ext      int32   `json:"ext,omitempty"`
	Soul     int     `json:"soul,omitempty"`
	TLGrade  [3]int  `json:"tlgrade"`
	TJ       [3]int  `json:"tj"`
	Relate   []int32 `json:"relate,omitempty"`
	Together []int32 `json:"together,omitempty"`
}

type lxGearJ struct {
	M, W, S       int32
	ML, WL, SL    int
	MTJ, WTJ, STJ int
}

func (x *lxPet) detailRec(pos int, armed []int32) lxDetail {
	d := lxDetail{PetType: x.pet.Type, Pos: pos, Grade: x.grade, At: x.att, HP: x.hp, Sp: x.speed, TfmID: x.pi.Magic,
		TfmLv: x.pet.magicLv(), Armed: slices.Clone(armed), Soul: x.pet.SoulGrade, TJ: x.tj, Relate: x.pi.Relate,
		Together: x.pet.Together,
		Gear: lxGearJ{x.gear.magic, x.gear.weapon, x.gear.shirt, x.gear.magicLv, x.gear.weaponLv, x.gear.shirtLv,
			x.gear.magicTJ, x.gear.weaponTJ, x.gear.shirtTJ}}
	for i := range 3 {
		d.TLGrade[i] = x.pet.shownTalGrade(i)
	}
	for i, t := range x.tals {
		if i >= 3 {
			d.Ext = t
		} else {
			d.Orgin = append(d.Orgin, t)
		}
	}
	return d
}

func (x *lxPet) detail(pos int, armed []int32) xym.Msg { return x.detailRec(pos, armed).msg() }

func (d lxDetail) msg() xym.Msg {
	at := []xym.Msg{}
	for i, t := range d.Armed {
		if t != 0 {
			at = append(at, xym.Msg{"pos": i + 1, "tal": t})
		}
	}
	org := []xym.Msg{}
	for _, t := range d.Orgin {
		org = append(org, xym.Msg{"tal": t})
	}
	rel := []xym.Msg{}
	for _, id := range d.Relate {
		rel = append(rel, xym.Msg{"rid": id, "lighten": 1, "together_open": b2i(slices.Contains(d.Together, id))})
	}
	g := d.Gear
	return xym.Msg{"pet_type": d.PetType, "pos": d.Pos, "grade": d.Grade, "at": d.At, "hp": d.HP, "sp": d.Sp,
		"tfmid": d.TfmID, "tfmlv": d.TfmLv, "rmid": g.M, "rmlv": g.ML, "rmtj": g.MTJ, "rwid": g.W, "rwlv": g.WL, "rwtj": g.WTJ,
		"rsid": g.S, "rslv": g.SL, "rstj": g.STJ, "armed_tal": at, "orgin_tal": org, "soul_grade": d.Soul,
		"tlgrade1": d.TLGrade[0], "tlgrade2": d.TLGrade[1], "tlgrade3": d.TLGrade[2],
		"tlxlv1": d.TJ[0], "tlxlv2": d.TJ[1], "tlxlv3": d.TJ[2], "ex_talent": d.Ext, "relate": rel}
}

// lxApplyStatFactors：战地因素里改初始属性的（命 / 攻 / 人 / 魔 / 仙 / 兽），双方都算，直接改进 Fighter（战报阵容
// 显示的就是加成后的数值）。
func lxApplyStatFactors(team []*battle.Fighter, factors []int) {
	for _, f := range team {
		attPct, hpPct := 0, 0
		for _, id := range factors {
			switch id {
			case battle.LxHP:
				hpPct += battle.LxFactorPct[id]
			case battle.LxAtt:
				attPct += battle.LxFactorPct[id]
			case battle.LxHuman, battle.LxGhost, battle.LxGod, battle.LxBeast:
				if f.Race == id-battle.LxHuman+1 {
					attPct += battle.LxFactorPct[id]
				}
			}
		}
		f.Att = int32(int64(f.Att) * int64(100+attPct) / 100)
		f.HP = int32(int64(f.HP) * int64(100+hpPct) / 100)
		f.MaxHP = int32(int64(f.MaxHP) * int64(100+hpPct) / 100)
	}
}

// lxTeam 是裂隙布阵的战斗单位（我方站位 1–8），算进属性类战地因素。
func (p *Player) lxTeam(team map[int]int32, armed map[int32][]int32, shop, bag []int32, tjPlus int, factors []int) []*battle.Fighter {
	cands := p.lxCandidates()
	var out []*battle.Fighter
	for pos := 1; pos <= lineupSlots; pos++ {
		pet := cands[team[pos]]
		if pet == nil {
			continue
		}
		if x := p.lxPetOf(pet, tjPlus); x != nil {
			out = append(out, x.fighter(pos, armed[pet.Type]))
		}
	}
	lxApplyStatFactors(out, factors)
	return out
}

// lxCtx 是一次请求操作的是哪套布阵 / 神通：副本 fb（> 0）或竞技场（0）。
type lxCtx struct {
	team  map[int]int32
	armed map[int32][]int32
	bag   []int32 // 可用的自选神通（副本：该副本掉落 + 增益兑换；竞技场：本周全部）
	fb    int
}

func (x *LiexiState) ctx(fb int) *lxCtx {
	if fb > 0 {
		r := x.run(fb)
		if r.Team == nil {
			r.Team = map[int]int32{}
		}
		return &lxCtx{team: r.Team, armed: r.Armed, bag: append(slices.Clone(r.Bag), x.Shop...), fb: fb}
	}
	if x.JTeam == nil {
		x.JTeam = map[int]int32{}
	}
	var bag []int32
	for fb := 1; fb <= len(lxFbDefs); fb++ {
		if r := x.Fbs[fb]; r != nil {
			bag = append(bag, r.Bag...)
		}
	}
	return &lxCtx{team: x.JTeam, armed: x.JArmed, bag: append(bag, x.Shop...)}
}

// bagUse 把 bag 里每一格分给装备它的护法（同一神通有多份时按顺序分），返回 bag_pos（1 起）-> 护法类型。
func (c *lxCtx) bagUse() map[int]int32 {
	used := map[int]int32{}
	types := make([]int32, 0, len(c.armed))
	for t := range c.armed {
		types = append(types, t)
	}
	slices.Sort(types)
	for _, pt := range types {
		for _, tal := range c.armed[pt] {
			if tal == 0 {
				continue
			}
			for i, b := range c.bag {
				if b == tal && used[i+1] == 0 {
					used[i+1] = pt
					break
				}
			}
		}
	}
	return used
}

func (s *Session) lxUpdateTeam(p *Player, fb int, m xym.Msg) {
	x := p.liexi()
	c := x.ctx(fb)
	cands := p.lxCandidates()
	team, _ := m["team"].([]xym.Msg)
	clear(c.team)
	seen := map[int32]bool{}
	for _, t := range team {
		pt, _ := t["pet_type"].(int64)
		pos, _ := t["pos"].(int64)
		if pos >= 1 && pos <= lineupSlots && cands[int32(pt)] != nil && !seen[int32(pt)] {
			c.team[int(pos)] = int32(pt)
			seen[int32(pt)] = true
		}
	}
	p.saveLog(s)
	var want []int
	q, _ := m["query"].([]xym.Msg)
	for _, x := range q {
		if pos, _ := x["pos"].(int64); pos > 0 {
			want = append(want, int(pos))
		}
	}
	s.lxSendTeam(p, c, want)
}

// lxSendTeam：S_FB_UPDATE_TEAM / S_JJC_UPDATE_TEAM（战力是整队，details 只发问到的站位）。
func (s *Session) lxSendTeam(p *Player, c *lxCtx, want []int) {
	x := p.liexi()
	cands := p.lxCandidates()
	fp := 0
	details := []xym.Msg{}
	for pos := 1; pos <= lineupSlots; pos++ {
		pet := cands[c.team[pos]]
		if pet == nil {
			continue
		}
		lp := p.lxPetOf(pet, x.TJPlus)
		if lp == nil {
			continue
		}
		fp += int(lp.att)*4 + int(lp.hp)
		if want == nil || slices.Contains(want, pos) {
			details = append(details, lp.detail(pos, c.armed[pet.Type]))
		}
	}
	if c.fb > 0 {
		s.SendExt("LIEXI_PTO.S_FB_UPDATE_TEAM", xym.Msg{"fb_id": c.fb, "fight_point": fp, "details": details})
	} else {
		s.SendExt("LIEXI_PTO.S_JJC_UPDATE_TEAM", xym.Msg{"fight_point": fp, "details": details})
	}
}

func (s *Session) lxQueryPet(p *Player, fb int, m xym.Msg) {
	x := p.liexi()
	c := x.ctx(fb)
	pt, _ := m["pet_type"].(int64)
	pet := p.lxCandidates()[int32(pt)]
	if pet == nil {
		return
	}
	lp := p.lxPetOf(pet, x.TJPlus)
	if lp == nil {
		return
	}
	pos := 0
	for k, v := range c.team {
		if v == pet.Type {
			pos = k
		}
	}
	d := lp.detail(pos, c.armed[pet.Type])
	if fb > 0 {
		d["fb_id"] = fb
		s.SendExt("LIEXI_PTO.S_FB_SINGLE_PET_INFO", d)
	} else {
		s.SendExt("LIEXI_PTO.S_JJC_SINGLE_PET_INFO", d)
	}
}

// lxTalList：给护法 pet_type 选神通的列表——自己装的、别人装的、没人装的。
func (s *Session) lxTalList(p *Player, fb int, m xym.Msg) {
	c := p.liexi().ctx(fb)
	pt, _ := m["pet_type"].(int64)
	use := c.bagUse()
	self, other, free := []xym.Msg{}, []xym.Msg{}, []xym.Msg{}
	for i, tal := range c.bag {
		owner := use[i+1]
		switch {
		case owner == 0:
			free = append(free, xym.Msg{"tal": tal, "bag_pos": i + 1})
		case owner == int32(pt):
			self = append(self, xym.Msg{"tal": tal, "bag_pos": i + 1, "pet_type": owner})
		default:
			other = append(other, xym.Msg{"tal": tal, "bag_pos": i + 1, "pet_type": owner})
		}
	}
	msg := xym.Msg{"self_armed": self, "other_armed": other, "not_armed": free}
	if fb > 0 {
		msg["fb_id"] = fb
		s.SendExt("LIEXI_PTO.S_FB_SEND_TAL_LIST", msg)
	} else {
		s.SendExt("LIEXI_PTO.S_JJC_SEND_TAL_LIST", msg)
	}
}

func (s *Session) lxTalBag(p *Player, fb int) {
	c := p.liexi().ctx(fb)
	use := c.bagUse()
	armed, free := []xym.Msg{}, []xym.Msg{}
	for i, tal := range c.bag {
		if owner := use[i+1]; owner != 0 {
			armed = append(armed, xym.Msg{"tal": tal, "bag_pos": i + 1, "pet_type": owner})
		} else {
			free = append(free, xym.Msg{"tal": tal, "bag_pos": i + 1})
		}
	}
	msg := xym.Msg{"armed": armed, "not_armed": free}
	if fb > 0 {
		msg["fb_id"] = fb
		s.SendExt("LIEXI_PTO.S_FB_SEND_TAL_BAG", msg)
	} else {
		s.SendExt("LIEXI_PTO.S_JJC_SEND_TAL_BAG", msg)
	}
}

// lxGroupOf 是神通 tal 所在的自选神通组。
func lxGroupOf(tal int32) *gamedata.LiexiTal {
	for i := range gamedata.LiexiTals {
		if slices.Contains(gamedata.LiexiTals[i].Members, tal) {
			return &gamedata.LiexiTals[i]
		}
	}
	return nil
}

func lxFamily(tal int32) int32 {
	if ti := gamedata.Talents[tal]; ti != nil {
		return ti.FamilyID
	}
	return 0
}

// lxArm：装 / 卸自选神通。装的时候检查冲突（客户端 liexi_tal_conflict：不能与列出的族同时装备，含自身的神通）。
func (s *Session) lxArm(p *Player, fb int, m xym.Msg, arm bool) {
	x := p.liexi()
	c := x.ctx(fb)
	pt64, _ := m["pet_type"].(int64)
	slot64, _ := m["armed_tal_pos"].(int64)
	pt, slot := int32(pt64), int(slot64)
	pet := p.lxCandidates()[pt]
	if pet == nil || slot < 1 || slot > lxTalSlots {
		return
	}
	cur := c.armed[pt]
	if len(cur) < lxTalSlots {
		cur = append(cur, make([]int32, lxTalSlots-len(cur))...)
	}
	if !arm {
		cur[slot-1] = 0
	} else {
		bp, _ := m["bag_pos"].(int64)
		if bp < 1 || int(bp) > len(c.bag) {
			return
		}
		tal := c.bag[bp-1]
		if owner := c.bagUse()[int(bp)]; owner != 0 && owner != pt {
			s.sendMsg("这个神通已经装备在其他护法身上了。")
			return
		}
		g := lxGroupOf(tal)
		lp := p.lxPetOf(pet, x.TJPlus)
		var have []int32
		if lp != nil {
			for _, t := range lp.tals {
				have = append(have, lxFamily(t))
			}
		}
		for i, t := range cur {
			if t != 0 && i != slot-1 {
				have = append(have, lxFamily(t))
			}
		}
		if g != nil {
			for _, f := range have {
				if slices.Contains(g.Conflict, f) {
					s.sendMsg("该神通与护法已有的神通冲突，不能同时装备。")
					return
				}
			}
		}
		cur[slot-1] = tal
	}
	c.armed[pt] = cur
	p.saveLog(s)
	pos := 0
	for k, v := range c.team {
		if v == pt {
			pos = k
		}
	}
	if pos > 0 {
		s.lxSendTeam(p, c, []int{pos})
	} else {
		s.lxQueryPet(p, fb, xym.Msg{"pet_type": int64(pt)})
	}
}
