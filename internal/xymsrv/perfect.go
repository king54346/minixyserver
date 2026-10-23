package xymsrv

import (
	"errors"
	"sort"

	"turnrpg/internal/gamedata"
)

// 测试用「完美账号」（管理接口 POST /perfect）：人物满级；每种护法一只，满级、满境界、天生法术满级、
// 三神通满（金边，同名数按升满所需的神通点倒推）、五星的第四神通三个候选全满；每种装备 / 法术书各一件，满强化，装备镶 9 级宝石、
// 炼神 10 级（武器、鞋突袭，衣服闪避，凑突袭套装），法术书精修满；4、5 星装备 / 法术天将全部神阶；
// 出战阵容放 8 只最强的五星护法并一键配装。已有存档会被整体覆盖（角色须离线）。

// PerfectSpec 是 POST /perfect 的参数。
type PerfectSpec struct {
	URS  string `json:"urs"`
	Pwd  string `json:"pwd"`
	Code string `json:"code"` // 安全码（新建账号时用）
	Name string `json:"name"` // 角色名（新建角色时用）
}

const (
	perfectCash = 1_500_000_000
	perfectGold = 9_999_999
)

// MakePerfect 建（或重置）一个完美账号，返回 uid。
func (srv *Server) MakePerfect(spec PerfectSpec) (int32, error) {
	acc, err := srv.accounts.Register(spec.URS, spec.Pwd, spec.Code)
	if errors.Is(err, ErrNameTaken) {
		acc, err = srv.accounts.Login(spec.URS, spec.Pwd, "")
	}
	if err != nil {
		return 0, err
	}
	uid := acc.UID
	if srv.sessionOf(uid) != nil {
		return 0, errors.New("role is online, log out first")
	}
	if srv.roles.Get(uid) == nil {
		name := spec.Name
		if name == "" {
			name = spec.URS
		}
		if _, err := srv.roles.Create(uid, 1, name); err != nil {
			return 0, err
		}
	}
	if _, err := srv.roles.Update(uid, func(ro *Role) bool {
		ro.Grade, ro.Exp = gamedata.MaxRoleGrade, 0
		ro.Cash, ro.Gold, ro.SoulExp, ro.FriendPoint = perfectCash, perfectGold, 999999, 999999
		ro.TPoint, ro.HPoint, ro.VIP = maxTPoint, maxHPoint, defaultVIP
		return true
	}); err != nil {
		return 0, err
	}
	return uid, srv.fillPerfectSave(uid)
}

func (srv *Server) fillPerfectSave(uid int32) error {
	p, err := srv.players.Get(uid)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.role = srv.roles.Get(uid)
	p.fillPerfect()
	return p.save()
}

// PerfectExisting 把已有角色（按 uid，或按账号名 urs）直接改成完美账号：在线的先踢下线。
// 注意：护法 / 装备 / 法术书会整体换成完美配置（原有的不保留），闯关进度等其余数据保留。
func (srv *Server) PerfectExisting(uid int32, urs string) (int32, error) {
	if uid != 0 && srv.roles.Get(uid) == nil {
		uid = 0 // 不是角色 uid（纯数字的账号名）：按账号名找
	}
	if uid == 0 && urs != "" {
		srv.accounts.mu.Lock()
		if acc := srv.accounts.ByURS[key(urs)]; acc != nil {
			uid = acc.UID
		}
		srv.accounts.mu.Unlock()
	}
	if uid == 0 || srv.roles.Get(uid) == nil {
		return 0, errors.New("no such role")
	}
	if err := srv.kickWait(uid); err != nil {
		return 0, err
	}
	if _, err := srv.roles.Update(uid, func(ro *Role) bool {
		ro.Grade, ro.Exp = gamedata.MaxRoleGrade, 0
		ro.Cash, ro.Gold, ro.SoulExp, ro.FriendPoint = perfectCash, perfectGold, 999999, 999999
		ro.TPoint, ro.HPoint, ro.VIP = maxTPoint, maxHPoint, defaultVIP
		return true
	}); err != nil {
		return 0, err
	}
	return uid, srv.fillPerfectSave(uid)
}

// perfectSameName：满神通要花的神通点里，境界 1–10 给的不够的部分由同名卡补（每张 shenTongPoint[星级]），
// 返回需要的同名卡数和剩下的神通点——和正常玩法升满一样，金边的卡都带同名数。
func perfectSameName(pet *Pet, pi *gamedata.PetInfo) (add, left int) {
	have := 0
	for g := 1; g <= maxSoulGrade; g++ {
		have += jingjiePoint(g, pi.Star)
	}
	need := spentPoints(pet, pi)
	if pi.Star < 1 || pi.Star > 5 || need <= have {
		return 0, max(have-need, 0)
	}
	per := shenTongPoint[pi.Star]
	add = (need - have + per - 1) / per
	return add, have + add*per - need
}

// fillPerfect 把存档换成完美配置（保留 uid、闯关进度等其余玩法数据）。
func (p *Player) fillPerfect() {
	p.Pets, p.Suits, p.Books = nil, nil, nil
	p.NextNpc, p.NextItemNum = firstNpcNum, firstItemNum
	p.BagLimit = [4]int{0, 600, 300, 300}

	var types []int32
	for t := range gamedata.Pets {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	for _, t := range types {
		pi := gamedata.Pets[t]
		pet := p.addPet(t)
		pet.Grade, pet.SoulGrade, pet.MagicLv = gamedata.MaxPetGrade, maxSoulGrade, maxMagicGrade
		for i, l := range pi.Talents {
			pet.TalGrade[i] = len(l)
		}
		pet.TLAdd, pet.TLPoint = perfectSameName(pet, pi)
		pet.AttAdd, pet.HPAdd = pointMax, pointMax // 培养满
		if pi.Star >= 5 && len(pi.ExTalents[0]) > 0 {
			pet.ExtOn, pet.ExtIdx = true, 1
			for i, l := range pi.ExTalents {
				pet.ExtGrade[i] = len(l)
			}
		}
	}

	tj := p.tj()
	var suitTypes []int
	for t, si := range gamedata.Suits {
		if _, ok := suitSlot[si.Kind]; ok && si.Prop != "" {
			suitTypes = append(suitTypes, t)
		}
	}
	sort.Ints(suitTypes)
	for _, t := range suitTypes {
		si := gamedata.Suits[t]
		su := p.addSuit(t)
		st := suitStar(t)
		su.Grade = min(len(suitUpExp[st])+1, gamedata.MaxRoleGrade)
		slot := suitSlot[si.Kind]
		su.Stone = gemBase[slot*2] + stoneMaxGrade // 太阳石 / 光芒石 / 神秘石 9 级
		if su.Grade >= lsFamilies[len(lsFamilies)-1].grade {
			su.Skill = 21621 // 突袭（炼神效果都是 1 级，套装等级由 9 级宝石激活：⌊27/3⌋ = 9）
			if slot == 1 {
				su.Skill = 21611 // 闪避
			}
		} else {
			su.ensureSkill()
		}
		if star := tjStar(tjTypeSuit, t); star > 0 {
			tj.Suit.entry(t).Lv, tj.Suit.entry(t).Points = tjMaxLv, tjCum(tjTypeSuit, star)[tjMaxLv]
		}
	}
	var bookTypes []int32
	for id := range gamedata.Magics {
		if id >= 11000 && id < 15000 {
			bookTypes = append(bookTypes, id)
		}
	}
	sort.Slice(bookTypes, func(i, j int) bool { return bookTypes[i] < bookTypes[j] })
	for _, t := range bookTypes {
		b := p.addBook(t)
		b.Grade = maxMagicGrade
		if gamedata.Magics[t].JXBuff != 0 {
			b.JX = maxMagicGrade
		}
		if star := tjStar(tjTypeMagic, int(t)); star > 0 {
			tj.Magic.entry(int(t)).Lv, tj.Magic.entry(int(t)).Points = tjMaxLv, tjCum(tjTypeMagic, star)[tjMaxLv]
		}
	}
	p.goldCount(tjTypeSuit)
	p.goldCount(tjTypeMagic)

	// 出战：8 只满级攻击×4+生命最高的五星
	var best []*Pet
	for _, pet := range p.Pets {
		if gamedata.Pets[pet.Type].Star == 5 {
			best = append(best, pet)
		}
	}
	power := func(pet *Pet) int32 {
		pi := gamedata.Pets[pet.Type]
		return pi.Att(pet.Grade)*4 + pi.HP(pet.Grade)
	}
	sort.SliceStable(best, func(i, j int) bool { return power(best[i]) > power(best[j]) })
	lu := &Lineup{No: 1, Leader: 1}
	for i := range lu.Slots {
		lu.Slots[i] = &Slot{}
		if i < len(best) {
			lu.Slots[i].Pet = best[i].BagPos
		}
	}
	p.Lineups, p.FightNo = []*Lineup{lu}, 1
	p.applyPlan(lu, p.planEquip(lu.Slots))

	p.Items = nil
	for _, it := range []struct {
		typ int
		n   int32
	}{{lianshenStone, 999}, {ningshenStone, 999}, {zaojingStone, 99}, {wuziTianshu, 99}, {yuhuoDan, 999}, {cuiYuanSha, 999}} {
		p.addItem(it.typ, it.n)
	}
	for _, e := range elementItems {
		p.addItem(e, 9999)
	}
	p.SweepTickets = 999
}
