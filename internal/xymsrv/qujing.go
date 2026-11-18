package xymsrv

import (
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 西天取经（QUJING_PTO，42001–42019）。客户端：地图下方「取经」→ view/QuJingView.lua（选难度 / 地图）、
// QuJingTeamView.lua（布阵、献祭、跳过、援助）、QuJingShopView.lua（经书兑换）、logic/qujing_mgr.lua。
// 抓包：analysis/captures/session-03.jsonl（困难难度打到第 5 劫）；资料与截图：analysis/data/取经/。
//
// 规则（参考资料 + 抓包 + 客户端）：
//   - 每周可进行 1 次（S_QUJING_OPEN_INIT.left）；开始时选普通（stage_type 1）或困难（2）。
//   - 九劫九难：第 1、4 难是 NPC 阵容，2/3/5/6 难是「取经人」（玩家阵容池：抓包的主流阵容 + 论道前十的胜场阵容），
//     第 7 难带特殊 AI（各劫不同，客户端 qujing_mgr.SpecialAIs），第 8 难打魂关（只打一场，十回合后无论胜负都去下一难，
//     打中敌人一次得一个落宝），第 9 难首领。打完一劫领宝箱（经书），再开下一劫。
//   - 所有五星护法都能上阵，不吃装备和法术书（护法等级补到人物等级×2、缘全亮，同时空裂隙）；双方血量都带到下一场，
//     阵亡的护法本次取经里不能再上，每天一次全体复活。
//   - 献祭满血护法换献祭点（15 + Σ神通等级×神通点数），用献祭点跳过第 2/3/5/6 难（各劫价格见 qjSkipCost）。
//   - 援助：花 10 元宝招募一名好友（没有好友时找别的玩家）的五星护法助战，最多 3 名，满了替换最早的。
//   - 经书（道具 10441）在商店兑换卡牌；经书加成（×1.5 / ×2 / ×3，按 VIP 买）作用于每劫宝箱。
//
// 估计的数值（抓包只有困难难度第 1–5 劫，标在常量旁）：普通难度敌人与宝箱、第 5–9 劫的阵容与属性、换阵容格价格。

const (
	qjNormal = 1
	qjHard   = 2

	qjOpen   = 1 // stage_state：本劫进行中
	qjFinish = 2 // 本劫首领已过，宝箱未领
	qjGotBox = 3 // 宝箱已领

	qjStages      = 9
	qjJingshu     = 10441 // 经书
	qjHelperBase  = 2000000
	qjHelperMax   = 3
	qjHelperGold  = 10
	qjLineupMax   = 3
	qjLineupGold  = 100 // 估计：多一个阵容格的价格
	qjWeeklyTimes = 1
)

// qjSkipCost[难度][劫-1]：跳过一难要的献祭点（客户端 QuJingTeamView CQuJingSkipView）。
var qjSkipCost = map[int][qjStages]int{
	qjNormal: {200, 300, 400, 500, 650, 850, 1100, 1450, 1950},
	qjHard:   {250, 350, 450, 600, 800, 1050, 1400, 1800, 2400},
}

// qjBookAdd：经书加成（客户端 QuJingBookAddView）——idx → 价格、VIP 范围、倍率。
var qjBookAdd = map[int]struct {
	gold, vipFrom, vipTo int
	mult                 float64
	text                 string
}{1: {300, 6, 9, 1.5, "1.5"}, 2: {600, 10, 14, 2, "2"}, 3: {1200, 15, 15, 3, "3"}}

// 敌人属性：NPC / 特殊 / 打魂 / 首领用官方怪物表的公式（qjNpcFighter，参数在 qujing.json）；
// 取经人（玩家阵容） = 基础属性 × 1.05 × 1.13^(劫-1)，第 4 劫以后每劫只 × 1.04（抓包拟合：同一护法第 5、6 劫
// 比第 4 劫只高 0–7%）。普通难度 × 0.8（估计）。
func qjPlayerScale(stage int) float64 {
	return 1.05 * math.Pow(1.13, float64(min(stage, 4)-1)) * math.Pow(1.04, float64(max(stage-4, 0)))
}

func qjDiffScale(typ int) float64 {
	if typ == qjNormal {
		return 0.8
	}
	return 1
}

// qjBoxBase 是每劫宝箱的经书（困难 30×劫，抓包加成 ×3 时 90/180/270/360；普通 20×劫，估计）。
func qjBoxBase(typ, stage int) int {
	if typ == qjNormal {
		return 20 * stage
	}
	return 30 * stage
}

// QujingState 是玩家的西天取经存档（每周刷新）。
type QujingState struct {
	Week      int            `json:"week"`
	Left      int            `json:"left"`             // 本周还能开始几次
	Active    bool           `json:"active,omitempty"` // 正在取经
	Type      int            `json:"type,omitempty"`   // 难度
	Stage     int            `json:"stage,omitempty"`
	Sub       int            `json:"sub,omitempty"`
	State     int            `json:"state,omitempty"`
	BookAdd   int            `json:"book_add,omitempty"` // 经书加成 idx（0 = 没买）
	Xianji    int            `json:"xianji,omitempty"`
	PetHP     map[int]int64  `json:"pet_hp,omitempty"` // bag_pos → 剩余生命（没有 = 满血）
	Helpers   []*qjHelper    `json:"helpers,omitempty"`
	HelperSeq int            `json:"helper_seq,omitempty"`
	HelpBuys  int            `json:"help_buys,omitempty"` // 本周招募援助次数（提示里的「本周第 N 次」）
	ReliveDay string         `json:"relive_day,omitempty"`
	Lineups   [][]qjSlot     `json:"lineups,omitempty"`
	CurLineup int            `json:"cur_lineup,omitempty"` // 1 起
	Foe       *qjFoe         `json:"foe,omitempty"`        // 当前这一难的敌人（血量跨战斗保留）
	Bought    map[int]int    `json:"bought,omitempty"`     // 商店 pos → 本周已兑换
	Die       map[int]int    `json:"die,omitempty"`        // 难度 → 本周阵亡护法数（排行榜）
	Best      map[int][2]int `json:"best,omitempty"`       // 难度 → 本周最远的 {劫, 难}
	Fights    int            `json:"fights,omitempty"`
}

type qjSlot struct {
	FightPos int `json:"fight_pos"`
	BagPos   int `json:"bag_pos"`
}

// qjHelper 是一名援助护法（use_type 2，bag_pos = 2000000 + 序号）。
type qjHelper struct {
	BagPos  int             `json:"bag_pos"`
	Owner   string          `json:"owner"`
	Fighter *battle.Fighter `json:"fighter"`
	HP      int64           `json:"hp"`
	Card    *qjCard         `json:"card,omitempty"` // 原卡的境界 / 同名 / 神通等级（没有 = 按满配显示）
}

// qjCard 是卡面信息（S_QUJING_OPEN_SUB_STAGE_MAIN 的 sgrade 境界、add_tlpoint 同名、tlgrade1-3、extalent 第四神通）。
type qjCard struct {
	SGrade   int    `json:"sgrade"`
	TLAdd    int    `json:"tl_add"`
	TLGrade  [3]int `json:"tlgrade"`
	ExTalent int32  `json:"extalent,omitempty"`
}

// fullCard 是满配的卡面（抓包阵容里借来的援助：境界 10、神通满级）。
func fullCard(typ int32) *qjCard {
	c := &qjCard{SGrade: 10}
	if pi := gamedata.Pets[typ]; pi != nil {
		for i := range 3 {
			c.TLGrade[i] = len(pi.Talents[i])
		}
	}
	return c
}

// qjFoe 是一难的敌人。
type qjFoe struct {
	Stage, Sub int
	Name, Desc string
	Icon       int32
	Level      int
	Team       []*battle.Fighter
}

func (p *Player) qujing() *QujingState {
	if p.Qujing == nil {
		p.Qujing = &QujingState{}
	}
	q := p.Qujing
	if wk := lxWeekOf(lxNow()); q.Week != wk {
		lineups, cur := q.Lineups, q.CurLineup // 阵容格是买的，跨周保留
		*q = QujingState{Week: wk, Left: qjWeeklyTimes, Lineups: lineups, CurLineup: cur}
	}
	if len(q.Lineups) == 0 {
		q.Lineups, q.CurLineup = [][]qjSlot{{}}, 1
	}
	q.CurLineup = min(max(q.CurLineup, 1), len(q.Lineups))
	if q.PetHP == nil {
		q.PetHP = map[int]int64{}
	}
	return q
}

func init() {
	Handle("QUJING_PTO.C_QUJING_OPEN_MAIN", func(s *Session, p *Player, _ xym.Msg) { s.qjMain(p) })
	Handle("QUJING_PTO.C_QUJING_SELECT_STAGE_TYPE", onQjSelect)
	Handle("QUJING_PTO.C_QUJING_OPEN_SUB_STAGE_MAIN", func(s *Session, p *Player, _ xym.Msg) { s.qjSubMain(p) })
	Handle("QUJING_PTO.C_QUJING_START_FIGHT", onQjFight)
	Handle("QUJING_PTO.C_QUJING_RELIVE_ALL_PETS", onQjRelive)
	Handle("QUJING_PTO.C_QUJING_TRANSFORM_PETS_TO_XIANJI", onQjSacrifice)
	Handle("QUJING_PTO.C_QUJING_SKIP_SUB_STAGE", onQjSkip)
	Handle("QUJING_PTO.C_QUJING_CLICK_OPEN_BOX", onQjBox)
	Handle("QUJING_PTO.C_QUJING_OPEN_NEXT_STAGE", onQjNext)
	Handle("QUJING_PTO.C_QUJING_OPEN_GOODS_LIST", func(s *Session, p *Player, _ xym.Msg) { s.qjGoods(p) })
	Handle("QUJING_PTO.C_QUJING_BUY_GOODS", onQjBuy)
	Handle("QUJING_PTO.C_QUJING_BUY_JINGSHU_ADD", onQjBookAdd)
	Handle("QUJING_PTO.C_QUJING_GET_LINEUP_LIST_INFO", func(s *Session, p *Player, _ xym.Msg) { s.qjLineups(p) })
	Handle("QUJING_PTO.C_QUJING_SAVE_LINEUP_LIST", onQjSaveLineups)
	Handle("QUJING_PTO.C_QUJING_BUY_LINEUP_NO", onQjBuyLineup)
	Handle("QUJING_PTO.C_QUJING_USE_HELP_DATA", onQjHelp)
	Handle("QUJING_PTO.C_QUJING_ALL_RANK", func(s *Session, p *Player, _ xym.Msg) { s.qjRank(p) })
	Handle("QUJING_PTO.C_QUJING_TRIAL_STAGE_RANK", onQjTrialRank)
	Handle("QUJING_PTO.C_QUJING_TRIAL_STAGE_RANK_PLAY_RECORD", onQjTrialPlay)
	// 突发事件「西天取经进行中！」（type 112，客户端 refreshQuJing，点「前往」打开取经；抓包 session-04）：本周还能取经 / 正在取经时有
	regEvent(func(p *Player, m xym.Msg) {
		if q := p.qujing(); q.Active || q.Left > 0 {
			addEvent(m, 112, nil)
		}
	})
}

// ---- 主界面 ----

func (s *Session) qjMain(p *Player) {
	q := p.qujing()
	if !q.Active {
		s.SendExt("QUJING_PTO.S_QUJING_OPEN_INIT", xym.Msg{"left": q.Left, "type": 1})
		return
	}
	s.qjStageMain(p)
}

func (s *Session) qjStageMain(p *Player) {
	q := p.qujing()
	add := ""
	if b, ok := qjBookAdd[q.BookAdd]; ok {
		add = b.text
	}
	next := 0
	if q.Stage < qjStages {
		next = 1
	}
	s.SendExt("QUJING_PTO.S_QUJING_OPEN_STAGE_MAIN", xym.Msg{"type": 1, "stage": q.Stage, "sub_stage": q.Sub,
		"stage_state": q.State, "jingshu_add": add, "can_open_next_stage": next})
}

func onQjSelect(s *Session, p *Player, m xym.Msg) {
	q := p.qujing()
	typ, _ := m["stage_type"].(int64)
	if q.Active {
		s.qjStageMain(p)
		return
	}
	if q.Left <= 0 {
		s.retMsg("本周的取经次数已用完，下周再来吧！")
		return
	}
	if typ != qjNormal && typ != qjHard {
		typ = qjNormal
	}
	q.Left--
	q.Active, q.Type, q.Stage, q.Sub, q.State, q.Foe = true, int(typ), 1, 1, qjOpen, nil
	p.saveLog(s)
	s.qjStageMain(p)
}

// ---- 护法 ----

// qjPet 是一名可上阵的护法（自己的五星护法或援助护法）。
type qjPet struct {
	bagPos  int
	useType int
	pet     *Pet
	f       *battle.Fighter // 满血时的战斗单位（站位在上阵时改）
	hp      int64
	card    *qjCard // 援助的卡面
}

// qjFighterOf 是自己的护法在取经里的战斗单位：等级补到人物等级×2、缘全亮，不吃装备与法术书。
func (p *Player) qjFighterOf(pet *Pet) *battle.Fighter {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return nil
	}
	grade := p.lxGradeOf(pet)
	att := float64(pi.Att(grade)+int32(pet.AttAdd)) * lxRelateRatio(pet, pi, false) * p.extendRatio(pet, false)
	hp := float64(pi.HP(grade)+int32(pet.HPAdd*pointHPStep)) * lxRelateRatio(pet, pi, true) * p.extendRatio(pet, true)
	tals, tj := p.activeTalents(pet, pi)
	return &battle.Fighter{ID: pet.NpcNum, Name: pi.Name, Att: int32(att), HP: int32(hp), MaxHP: int32(hp),
		Speed: p.petSpeed(pet), Icon: pet.Type, Race: pi.Race, Grade: grade, Talents: tals, TalentTJ: tj,
		Magic: pi.Magic, MagicLv: pet.magicLv(), SkillFull: pet.talentsFull(pi)}
}

// qjPets 是全部可选护法（bag_pos → qjPet）：自己的五星护法 + 援助护法。
func (p *Player) qjPets() map[int]*qjPet {
	q := p.qujing()
	out := map[int]*qjPet{}
	for _, pet := range p.Pets {
		pi := gamedata.Pets[pet.Type]
		if pi == nil || pi.Star != 5 {
			continue
		}
		f := p.qjFighterOf(pet)
		if f == nil {
			continue
		}
		hp := int64(f.MaxHP)
		if v, ok := q.PetHP[pet.BagPos]; ok {
			hp = min(v, hp)
		}
		out[pet.BagPos] = &qjPet{bagPos: pet.BagPos, useType: 1, pet: pet, f: f, hp: hp}
	}
	for _, h := range q.Helpers {
		out[h.BagPos] = &qjPet{bagPos: h.BagPos, useType: 2, f: h.Fighter, hp: h.HP, card: h.Card}
	}
	return out
}

func (x *qjPet) msg() xym.Msg {
	m := xym.Msg{"type": x.f.Icon, "use_type": x.useType, "bag_pos": x.bagPos, "hp": x.hp, "max_hp": x.f.MaxHP,
		"grade": x.f.Grade, "add_tlpoint": 0, "sgrade": 0, "tlgrade1": 0, "tlgrade2": 0, "tlgrade3": 0,
		"tal_magic_lev": max(x.f.MagicLv, 1), "att": x.f.Att, "extalent": 0}
	if pet := x.pet; pet != nil {
		m["add_tlpoint"], m["sgrade"] = pet.TLAdd, pet.SoulGrade
		for i := range 3 {
			m[fmt.Sprintf("tlgrade%d", i+1)] = max(pet.TalGrade[i], 1)
		}
		if pi := gamedata.Pets[pet.Type]; pi != nil {
			m["extalent"] = pet.extTalent(pi)
		}
	} else {
		c := x.card
		if c == nil {
			c = fullCard(x.f.Icon)
		}
		m["add_tlpoint"], m["sgrade"], m["extalent"] = c.TLAdd, c.SGrade, c.ExTalent
		for i := range 3 {
			m[fmt.Sprintf("tlgrade%d", i+1)] = c.TLGrade[i]
		}
	}
	return m
}

// ---- 敌人 ----

// qjTemplate 是第 stage 劫第 sub 难的官方阵型（客户端 fight_ca / fight_npc，export_qujing.py 导出）。
func qjTemplate(stage, sub int) *gamedata.QujingStage {
	for i := range gamedata.Qujing.Stages {
		if st := &gamedata.Qujing.Stages[i]; st.Stage == stage && st.Sub == sub {
			return st
		}
	}
	return nil
}

var qjNamesA = []rune("长短远近孤独逍遥慕容欧阳司空南宫上官诸葛轩辕令狐")
var qjNamesB = []string{"行者", "取经人", "小和尚", "居士", "游僧", "散人", "书生", "剑客", "道童", "旅人"}

// qjFoeFor 生成第 stage 劫第 sub 难的敌人（rng 种子含 uid：每个人抽到的不一样；同一周同一难每次都一样）。
// pool 是阵容池里别人的阵容（qjPoolFor），取经人从中抽。
func qjFoeFor(uid int32, week, typ, stage, sub int, pool []qjPoolEntry) *qjFoe {
	rng := rand.New(rand.NewPCG(uint64(uid)*1000003+uint64(week)*97+uint64(typ), uint64(stage*10+sub)))
	scale := qjDiffScale(typ)
	foe := &qjFoe{Stage: stage, Sub: sub, Level: 1}
	switch sub {
	case 2, 3, 5, 6: // 取经人：阵容池（起初是抓包的 71 套主流 PVP 阵容，之后由论道前十的胜场阵容更新）
		cands := pool
		if len(cands) == 0 {
			cands = qjCaptured()
		}
		if len(cands) == 0 {
			return foe
		}
		t := cands[rng.IntN(len(cands))]
		foe.Name, foe.Level, foe.Icon = t.Name, max(t.Grade, 1), int32(t.Icon)
		if foe.Name == "" { // 抓包阵容不用真实玩家名，随机起一个
			foe.Name = string(qjNamesA[rng.IntN(len(qjNamesA))]) + string(qjNamesA[rng.IntN(len(qjNamesA))]) + qjNamesB[rng.IntN(len(qjNamesB))]
			foe.Level, foe.Icon = 50, int32(1+rng.IntN(2))
		}
		foe.Desc = fmt.Sprintf("等级%d，第%d劫第%d难的取经人", foe.Level, stage, sub)
		for _, m := range t.Members {
			if f := qjMemberFighter(m, qjPlayerScale(stage)*scale); f != nil {
				foe.Team = append(foe.Team, f)
			}
		}
	default: // NPC / 特殊 / 打魂 / 首领：官方阵型，属性 = 基础 + 成长 × 参数 / 1000
		st := qjTemplate(stage, sub)
		if st == nil {
			return foe
		}
		foe.Name, foe.Desc, foe.Icon = st.Name, st.Desc, st.Icon
		for _, m := range st.Members {
			if f := qjNpcFighter(m, st.Param, scale); f != nil {
				foe.Team = append(foe.Team, f)
			}
		}
	}
	return foe
}

// qjMemberFighter 是玩家阵容里的一名护法：护法 100 级的基础属性 × k（神通、法术照阵容）。
func qjMemberFighter(m gamedata.QujingMember, k float64) *battle.Fighter {
	pi := gamedata.Pets[m.Type]
	if pi == nil {
		return nil
	}
	speed := m.Speed
	if speed == 0 {
		speed = pi.Speed
	}
	att, hp := float64(pi.Att(100))*k, float64(pi.HP(100))*k
	return &battle.Fighter{ID: int32(900000 + m.Pos), Pos: 100 + m.Pos, Name: pi.Name, Att: int32(att), HP: int32(hp),
		MaxHP: int32(hp), Speed: speed, Icon: m.Type, Race: pi.Race, Grade: 100, Talents: slices.Clone(m.Talents),
		Magic: m.Magic, MagicLv: max(m.MagicLv, 1), SkillFull: true}
}

// qjTransformNPCs 是首领变身用的怪（火人、宝藏守卫、活死人），属性按这一难的参数算。
func qjTransformNPCs(typ, stage, sub int) map[int32]*battle.Fighter {
	st := qjTemplate(stage, sub)
	if st == nil || len(gamedata.Qujing.NPCs) == 0 {
		return nil
	}
	out := map[int32]*battle.Fighter{}
	for _, m := range gamedata.Qujing.NPCs {
		if f := qjNpcFighter(*m, st.Param, qjDiffScale(typ)); f != nil {
			out[m.NPC] = f
		}
	}
	return out
}

// qjNpcFighter 是官方怪物表里的一只怪：属性 = 基础 + 成长 × param / 1000（与闯关的怪同一公式，抓包逐个核对过），× k。
func qjNpcFighter(m gamedata.QujingMember, param int, k float64) *battle.Fighter {
	att := float64(int(m.Att)+int(m.AttAdd)*param/1000) * k
	hp := float64(int(m.HP)+int(m.HPAdd)*param/1000) * k
	return &battle.Fighter{ID: int32(900000 + m.Pos), Pos: 100 + m.Pos, Name: m.Name, Att: int32(att), HP: int32(hp),
		MaxHP: int32(hp), Speed: m.Speed, Icon: m.Type, Race: m.Race, Grade: 100, Talents: slices.Clone(m.Talents),
		Magic: m.Magic, MagicLv: max(m.MagicLv, 1)}
}

// foe 是当前这一难的敌人（第一次进这一难时生成，之后血量跨战斗保留；阵容池后来的变化不影响已生成的）。
func (q *QujingState) foe(uid int32, pool []qjPoolEntry) *qjFoe {
	if q.Foe == nil || q.Foe.Stage != q.Stage || q.Foe.Sub != q.Sub {
		q.Foe = qjFoeFor(uid, q.Week, q.Type, q.Stage, q.Sub, pool)
	}
	return q.Foe
}

// ---- 取经人阵容池 ----
//
// 池子最多 qjPoolMax 套，起初是抓包整理的 71 套主流 PVP 阵容；之后论道前十名每赢一场（挑战胜利、或被挑战时守住），
// 这套阵容就收进池子（同一名玩家只留最新一套），满了先挤掉最早的（抓包阵容最先退场）。全服数据 globalQujing，跨周保留。

const (
	qjPoolMax = 100 // 阵容池最多存多少套
	qjPoolTop = 10  // 论道前几名的胜场阵容收进池子
)

// qjPoolEntry 是阵容池里的一套阵容：论道前十的胜场阵容（UID / Name 有值），或抓包模板（UID 0、Name 空）。
type qjPoolEntry struct {
	UID     int32                   `json:"uid,omitempty"`
	Name    string                  `json:"name,omitempty"`
	Grade   int                     `json:"grade,omitempty"`
	Icon    int                     `json:"icon,omitempty"`
	Members []gamedata.QujingMember `json:"members"`
}

// qjCaptured 是抓包整理的主流 PVP 阵容（71 套）。
func qjCaptured() []qjPoolEntry {
	out := make([]qjPoolEntry, 0, len(gamedata.Qujing.Pvp))
	for _, t := range gamedata.Qujing.Pvp {
		out = append(out, qjPoolEntry{Members: t.Members})
	}
	return out
}

// qjPool 返回阵容池（第一次用时放进抓包阵容）；调用方持 qjRankMu。
func (srv *Server) qjPool() *qjRankBoard {
	b := srv.qjRankBoard()
	if !b.PoolInit {
		b.PoolInit = true
		b.Pool = qjCaptured() // 旧版本按取经胜场收的阵容不要了
	}
	return b
}

// qjPoolFor 是阵容池里除 uid 自己以外的阵容。
func (srv *Server) qjPoolFor(uid int32) []qjPoolEntry {
	srv.qjRankMu.Lock()
	defer srv.qjRankMu.Unlock()
	b := srv.qjPool()
	out := make([]qjPoolEntry, 0, len(b.Pool))
	for _, e := range b.Pool {
		if e.UID != uid || uid == 0 {
			out = append(out, e)
		}
	}
	return out
}

// qjPoolAdd：论道前十名赢了一场，把这套阵容收进池子（同一名玩家只留最新一套，满了挤掉最早的）。
func (srv *Server) qjPoolAdd(uid int32, name string, grade, icon int, team []*battle.Fighter) {
	if uid == 0 || len(team) == 0 {
		return
	}
	e := qjPoolEntry{UID: uid, Name: name, Grade: grade, Icon: icon}
	for _, f := range team {
		e.Members = append(e.Members, gamedata.QujingMember{Pos: f.Pos % 100, Type: f.Icon, Speed: f.Speed, Magic: f.Magic,
			MagicLv: f.MagicLv, Talents: slices.Clone(f.Talents)})
	}
	srv.qjRankMu.Lock()
	defer srv.qjRankMu.Unlock()
	b := srv.qjPool()
	b.Pool = slices.DeleteFunc(b.Pool, func(x qjPoolEntry) bool { return x.UID == uid })
	b.Pool = append(b.Pool, e)
	if n := len(b.Pool); n > qjPoolMax {
		b.Pool = b.Pool[n-qjPoolMax:]
	}
	saveGlobal(srv.players.st, globalQujing, b)
}

// ---- 布阵界面 ----

func (s *Session) qjSubMain(p *Player) {
	q := p.qujing()
	if !q.Active || q.State != qjOpen {
		s.qjMain(p)
		return
	}
	foe := q.foe(p.UID, s.srv.qjPoolFor(p.UID))
	vs := []xym.Msg{}
	for _, f := range foe.Team {
		if f.HP > 0 {
			vs = append(vs, xym.Msg{"fight_pos": f.Pos - 100, "type": f.Icon, "hp": f.HP, "att": f.Att, "grade": f.Grade, "max_hp": f.MaxHP})
		}
	}
	pets := p.qjPets()
	fight := []xym.Msg{}
	inTeam := map[int]bool{}
	for _, sl := range q.Lineups[q.CurLineup-1] {
		if x := pets[sl.BagPos]; x != nil && !inTeam[sl.BagPos] {
			inTeam[sl.BagPos] = true
			m := x.msg()
			m["fight_pos"] = sl.FightPos
			fight = append(fight, m)
		}
	}
	keys := make([]int, 0, len(pets))
	for k := range pets {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	sel := make([]xym.Msg, 0, len(keys))
	for _, k := range keys {
		sel = append(sel, pets[k].msg())
	}
	s.SendExt("QUJING_PTO.S_QUJING_OPEN_SUB_STAGE_MAIN", xym.Msg{"stage_type": q.Type, "curr_lineup_no": q.CurLineup,
		"xianji": q.Xianji, "vs_name": foe.Name, "vs_desc": foe.Desc, "vs_icon": foe.Icon, "vs_fight_list": vs,
		"fight_list": fight, "select_list": sel})
}

// ---- 战斗 ----

// qjSpecialOpts 是第 7 难的特殊 AI（客户端 SpecialAIs）。
func qjSpecialOpts(stage int) battle.Options {
	var o battle.Options
	switch stage {
	case 1, 2, 3, 4:
		o.QjRace = stage // 非人 / 魔 / 仙 / 兽族护法伤害减半
	case 5:
		o.Factors = []int{battle.LxCharge}
	case 6:
		o.Factors = []int{battle.LxDeath2}
	case 7:
		o.Factors = []int{battle.LxEnter2}
	case 8:
		o.Factors = []int{battle.LxRampage}
	case 9:
		o.QjHitDeath = 7
	}
	return o
}

// qjSpecialRevives 判断第 7 难胜利后该护法是否复活满血（与特殊 AI 对应的那类护法；第 9 劫全部）。
func qjSpecialRevives(stage int, f *battle.Fighter) bool {
	if stage <= 4 {
		return f.Race == stage
	}
	if stage == 9 {
		return true
	}
	kind := map[int]int{5: battle.KindCharge, 6: battle.KindDeath, 7: battle.KindEnter, 8: battle.KindRampage}[stage]
	return battle.HasTalentKind(f.Talents, kind)
}

func onQjFight(s *Session, p *Player, m xym.Msg) {
	q := p.qujing()
	if !q.Active || q.State != qjOpen {
		s.retMsg("请先开始取经。")
		return
	}
	pets := p.qjPets()
	list, _ := m["pos_list"].([]xym.Msg)
	var team1 []*battle.Fighter
	var used []*qjPet
	var slots []qjSlot
	seen := map[int]bool{}
	names := map[string]bool{} // 同一名护法（含不同星级、援助的）只能上一张
	for _, e := range list {
		bag, _ := e["bag_pos"].(int64)
		fp, _ := e["fight_pos"].(int64)
		x := pets[int(bag)]
		if x == nil || seen[int(bag)] || fp < 1 || fp > 8 || len(team1) >= 8 {
			continue
		}
		if x.hp <= 0 {
			s.retMsg(fmt.Sprintf("%d号战位护法已阵亡，请少侠钦点一名护法上阵或复活护法！", fp))
			return
		}
		name := x.f.Name
		if pi := gamedata.Pets[x.f.Icon]; pi != nil {
			name = pi.Name
		}
		if names[name] {
			s.retMsg(fmt.Sprintf("阵容中不能有相同的护法（%s）！", name))
			return
		}
		names[name] = true
		seen[int(bag)] = true
		f := *x.f
		f.Pos, f.HP = int(fp), int32(x.hp)
		team1 = append(team1, &f)
		used = append(used, x)
		slots = append(slots, qjSlot{FightPos: int(fp), BagPos: int(bag)})
	}
	if len(team1) == 0 {
		s.retMsg("请至少上阵一名护法！")
		return
	}
	q.Lineups[q.CurLineup-1] = slots // 打过的阵容存为当前阵容（下次进来默认这一套）
	foe := q.foe(p.UID, s.srv.qjPoolFor(p.UID))
	var team2 []*battle.Fighter
	var foeIdx []int
	for i, f := range foe.Team {
		if f.HP > 0 {
			team2 = append(team2, battle.CloneTeam([]*battle.Fighter{f})[0])
			foeIdx = append(foeIdx, i)
		}
	}
	if len(team2) == 0 { // 敌人都没了（不该出现），直接算过
		s.qjAdvance(p, q)
		return
	}
	opts := battle.Options{}
	if q.Sub == 7 {
		opts = qjSpecialOpts(q.Stage)
	}
	opts.QjNPCs = qjTransformNPCs(q.Type, q.Stage, q.Sub)
	w2 := 0
	if foe.Level >= 50 {
		w2 = foe.Level * 10
	}
	opts.Prestige = [2]int{s.prestige(), w2}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulateWith(team1, team2, opts, s.actRng())
	q.Fights++
	rid := fmt.Sprintf("qujing-%d-%s-%d-%d-%d", p.UID, time.Now().Format("2006-01-02"), q.Stage, q.Sub, q.Fights)
	s.fightRecordW(rid, foe.Name, foe.Level, w2, r1, r2, res.Record)

	// 血量带到下一场
	for i, x := range used {
		hp := int64(0)
		if i < len(res.HP[0]) {
			hp = res.HP[0][i]
		}
		if hp <= 0 && x.hp > 0 {
			q.Die = qjInc(q.Die, q.Type)
		}
		p.qjSetHP(x, hp)
	}
	for i, idx := range foeIdx {
		if i < len(res.HP[1]) {
			foe.Team[idx].HP = int32(res.HP[1][i])
		}
	}
	win := res.Win
	rw := rewardMsg(3, nil)
	if win {
		rw = rewardMsg(2, nil)
	}
	rw["record"], rw["star"] = rid, 1
	d := &bagDelta{}
	soulGate := q.Sub == 8
	if soulGate { // 打魂关：打中一次一个落宝，换银两 / 魂值；胜利另给经书
		hits := res.Hits[0]
		cash, soul := int32(hits*2000), int32(hits*25)
		s.retMsg(fmt.Sprintf("本次打魂获得落宝%d个。", hits))
		s.addCash(cash)
		s.roleUpdate(func(ro *Role) bool { ro.SoulExp += soul; return true })
		rw["cash"], rw["soul"] = cash, soul
		s.qjTrialOffer(p, q.Stage, hits, &gateRecord{UID: p.UID, Name: s.role.Name, Grade: s.role.Grade, W1: s.prestige(),
			Name2: foe.Name, T1: r1, T2: r2, Record: res.Record})
		if win {
			n := 3
			if q.Stage == 1 {
				n = 1
			}
			d.items = append(d.items, p.addItem(qjJingshu, int32(n))...)
			d.gains = append(d.gains, gain{rtItem, qjJingshu, int32(n)})
		}
	}
	if win && q.Sub == 7 { // 特殊关胜利：对应的护法复活满血
		for i, x := range used {
			if qjSpecialRevives(q.Stage, team1[i]) {
				p.qjSetHP(x, int64(x.f.MaxHP))
			}
		}
	}
	if len(d.items) > 0 || len(d.gains) > 0 {
		rw["list"] = s.sendDelta(p, d)
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", rw)
	if win || soulGate {
		s.qjAdvance(p, q)
		return
	}
	p.saveLog(s)
	s.qjSubMain(p)
}

func qjInc(m map[int]int, k int) map[int]int {
	if m == nil {
		m = map[int]int{}
	}
	m[k]++
	return m
}

func (p *Player) qjSetHP(x *qjPet, hp int64) {
	q := p.qujing()
	x.hp = hp
	if x.useType == 2 {
		for _, h := range q.Helpers {
			if h.BagPos == x.bagPos {
				h.HP = hp
			}
		}
		return
	}
	if hp >= int64(x.f.MaxHP) {
		delete(q.PetHP, x.bagPos)
	} else {
		q.PetHP[x.bagPos] = max(hp, 0)
	}
}

// qjAdvance：过了这一难——去下一难，第 9 难过了本劫完成（领宝箱）。
func (s *Session) qjAdvance(p *Player, q *QujingState) {
	q.Foe = nil
	if q.Sub >= 9 {
		q.State, q.Sub = qjFinish, 10
	} else {
		q.Sub++
	}
	if q.Best == nil {
		q.Best = map[int][2]int{}
	}
	if b := q.Best[q.Type]; q.Stage > b[0] || q.Stage == b[0] && min(q.Sub, 9) > b[1] {
		q.Best[q.Type] = [2]int{q.Stage, min(q.Sub, 9)}
	}
	p.saveLog(s)
	s.srv.qjRankUpdate(p, s.role)
	s.qjStageMain(p)
}

// ---- 复活、献祭、跳过 ----

func onQjRelive(s *Session, p *Player, _ xym.Msg) {
	q := p.qujing()
	if q.ReliveDay == today() {
		s.retMsg("今天的护法全员复活次数已用完，明天再来吧！")
		return
	}
	q.ReliveDay = today()
	q.PetHP = map[int]int64{}
	for _, h := range q.Helpers {
		h.HP = int64(h.Fighter.MaxHP)
	}
	p.saveLog(s)
	s.retMsg("全体护法已复活，生命全满。")
	s.qjSubMain(p)
}

// qjSacrificePoints：献祭点 = 15 + Σ 神通等级 × 该神通的神通点数（含第四神通，客户端 calc_skip_point）。
func qjSacrificePoints(pet *Pet) int {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return 0
	}
	sum := 15
	for i, l := range pi.Talents {
		if i < 3 && len(l) > 0 && pet.TalGrade[i] > 0 {
			if ti := gamedata.Talents[l[0]]; ti != nil {
				sum += pet.TalGrade[i] * ti.Need
			}
		}
	}
	if ext := pet.extTalent(pi); ext != 0 && pet.ExtIdx >= 1 {
		if ti := gamedata.Talents[ext]; ti != nil {
			sum += pet.ExtGrade[pet.ExtIdx-1] * ti.Need
		}
	}
	return sum
}

func onQjSacrifice(s *Session, p *Player, m xym.Msg) {
	q := p.qujing()
	pets := p.qjPets()
	list, _ := m["bag_pos_list"].([]xym.Msg)
	inTeam := map[int]bool{}
	for _, sl := range q.Lineups[q.CurLineup-1] {
		inTeam[sl.BagPos] = true
	}
	n, pts := 0, 0
	for _, e := range list {
		bag, _ := e["bag_pos"].(int64)
		x := pets[int(bag)]
		if x == nil || x.useType != 1 || x.hp < int64(x.f.MaxHP) || inTeam[int(bag)] {
			continue
		}
		pts += qjSacrificePoints(x.pet)
		p.qjSetHP(x, 0)
		n++
	}
	if n == 0 {
		s.retMsg("请选择需要献祭的护法（只有满血、不在阵容里的护法可以献祭）。")
		return
	}
	q.Xianji += pts
	p.saveLog(s)
	s.SendExt("QUJING_PTO.S_QUJING_FINISH_TRANSFORM_PETS_TO_XIANJI", xym.Msg{"xianji": q.Xianji})
	s.retMsg(fmt.Sprintf("献祭了%d名护法，获得献祭点%d点。", n, pts))
}

func onQjSkip(s *Session, p *Player, _ xym.Msg) {
	q := p.qujing()
	if !q.Active || q.State != qjOpen || !slices.Contains([]int{2, 3, 5, 6}, q.Sub) {
		s.retMsg("每劫只有第2/3/5/6难可以跳过。")
		return
	}
	cost := qjSkipCost[q.Type][q.Stage-1]
	if q.Xianji < cost {
		s.retMsg(fmt.Sprintf("献祭点不足，跳过本劫需要%d点。", cost))
		return
	}
	q.Xianji -= cost
	q.Foe = nil
	q.Sub++
	p.saveLog(s)
	s.SendExt("QUJING_PTO.S_QUJING_FINISH_SKIP_SUB_STAGE", nil)
}

// ---- 宝箱、下一劫 ----

func onQjBox(s *Session, p *Player, _ xym.Msg) {
	q := p.qujing()
	if !q.Active || q.State != qjFinish {
		s.qjStageMain(p)
		return
	}
	n := qjBoxBase(q.Type, q.Stage)
	if b, ok := qjBookAdd[q.BookAdd]; ok {
		n = int(float64(n) * b.mult)
	}
	d := &bagDelta{}
	d.items = append(d.items, p.addItem(qjJingshu, int32(n))...)
	d.gains = append(d.gains, gain{rtItem, qjJingshu, int32(n)})
	q.State = qjGotBox
	if q.Stage >= qjStages {
		q.Active = false // 九劫取完
	}
	p.saveLog(s)
	rw := rewardMsg(1, s.sendDelta(p, d))
	rw["record"] = ""
	s.SendExt("ETC_PTO.S_REWARD_MSG", rw)
	if q.Active {
		s.qjStageMain(p)
	} else {
		s.retMsg("恭喜取得真经，本次西天取经完成！")
		s.qjMain(p)
	}
}

func onQjNext(s *Session, p *Player, _ xym.Msg) {
	q := p.qujing()
	if !q.Active || q.State != qjGotBox || q.Stage >= qjStages {
		if q.Active && q.State == qjFinish {
			s.retMsg("劫难已通过，请领奖励！")
		}
		s.qjStageMain(p)
		return
	}
	q.Stage, q.Sub, q.State, q.Foe = q.Stage+1, 1, qjOpen, nil
	p.saveLog(s)
	s.qjStageMain(p)
}

// ---- 商店、经书加成 ----

func (s *Session) qjGoods(p *Player) {
	q := p.qujing()
	books := p.itemCount(qjJingshu)
	l := make([]xym.Msg, 0, len(gamedata.Qujing.Shop))
	for _, g := range gamedata.Qujing.Shop {
		limit := max(g.Limit-q.Bought[g.Pos], 0)
		if g.Limit == 0 && g.Jingshu > 0 {
			limit = int(books) / g.Jingshu
		}
		l = append(l, xym.Msg{"pos": g.Pos, "id": g.ID, "type": g.Type, "name": g.Name,
			"limit": limit, "jingshu": g.Jingshu, "gold": g.Gold})
	}
	s.SendExt("QUJING_PTO.S_QUJING_OPEN_GOODS_LIST", xym.Msg{"jingshu": books, "list": l})
}

func onQjBuy(s *Session, p *Player, m xym.Msg) {
	q := p.qujing()
	pos, _ := m["pos"].(int64)
	n, _ := m["buy_num"].(int64)
	key, _ := m["price_key"].(string)
	n = max(n, 1)
	var g *gamedata.QujingGood
	for i := range gamedata.Qujing.Shop {
		if gamedata.Qujing.Shop[i].Pos == int(pos) {
			g = &gamedata.Qujing.Shop[i]
		}
	}
	switch {
	case g == nil:
		s.retMsg("没有这件商品。")
		return
	case g.Limit > 0 && n > int64(g.Limit-q.Bought[g.Pos]):
		s.retMsg("本周兑换次数已用完。")
		return
	}
	d := &bagDelta{}
	if key == "gold" {
		if g.Gold <= 0 || !s.spend(0, int32(g.Gold*int(n))) {
			s.retMsg("元宝不足。")
			return
		}
	} else {
		if g.Jingshu <= 0 || n > int64(p.itemCount(qjJingshu))/int64(g.Jingshu) {
			s.retMsg("经书不足。")
			return
		}
		cost := int32(int64(g.Jingshu) * n)
		changed, removed := p.takeItems(qjJingshu, cost)
		d.items, d.removed = changed, removed
	}
	if g.Limit > 0 {
		if q.Bought == nil {
			q.Bought = map[int]int{}
		}
		q.Bought[g.Pos] += int(n)
	}
	name, count := g.Name, n
	if g.Type == rtItem {
		itemID := int(g.ID)
		count = n * int64(g.Count)
		name = gamedata.Items[itemID].Name
		d.items = append(d.items, p.addItem(itemID, int32(count))...)
		d.gains = append(d.gains, gain{rtItem, itemID, int32(count)})
	} else {
		for range n {
			p.giveCard(g.ID, d)
		}
	}
	p.saveLog(s)
	rw := rewardMsg(1, s.sendDelta(p, d))
	rw["record"] = ""
	s.SendExt("ETC_PTO.S_REWARD_MSG", rw)
	s.retMsg(fmt.Sprintf("兑换成功，获得%s×%d。", name, count))
	s.qjGoods(p)
}

func onQjBookAdd(s *Session, p *Player, m xym.Msg) {
	q := p.qujing()
	idx, _ := m["idx"].(int64)
	b, ok := qjBookAdd[int(idx)]
	switch {
	case !ok:
		return
	case !q.Active:
		s.retMsg("请先开始取经。")
		return
	case q.BookAdd >= int(idx):
		s.retMsg("已经有同等或更高的经书加成了。")
		return
	case s.role.VIP < b.vipFrom:
		s.retMsg(fmt.Sprintf("VIP%d以上的用户才能购买。", b.vipFrom))
		return
	case !s.spend(0, int32(b.gold)):
		s.retMsg("元宝不足。")
		return
	}
	q.BookAdd = int(idx)
	p.saveLog(s)
	s.qjStageMain(p)
}

// ---- 阵容格 ----

func (s *Session) qjLineups(p *Player) {
	q := p.qujing()
	l := make([]xym.Msg, 0, len(q.Lineups))
	for i, lu := range q.Lineups {
		sl := make([]xym.Msg, 0, len(lu))
		for _, x := range lu {
			sl = append(sl, xym.Msg{"fight_pos": x.FightPos, "bag_pos": x.BagPos})
		}
		l = append(l, xym.Msg{"lineup_no": i + 1, "lineup": sl})
	}
	s.SendExt("QUJING_PTO.S_QUJING_LINEUP_LIST_INFO", xym.Msg{"max_no": len(q.Lineups), "curr_lineup_no": q.CurLineup, "list": l})
}

func onQjSaveLineups(s *Session, p *Player, m xym.Msg) {
	q := p.qujing()
	cur, _ := m["curr_lineup_no"].(int64)
	list, _ := m["list"].([]xym.Msg)
	for _, e := range list {
		no, _ := e["lineup_no"].(int64)
		if no < 1 || int(no) > len(q.Lineups) {
			continue
		}
		lu, _ := e["lineup"].([]xym.Msg)
		var slots []qjSlot
		for _, x := range lu {
			fp, _ := x["fight_pos"].(int64)
			bag, _ := x["bag_pos"].(int64)
			if fp >= 1 && fp <= 8 {
				slots = append(slots, qjSlot{FightPos: int(fp), BagPos: int(bag)})
			}
		}
		q.Lineups[no-1] = slots
	}
	if cur >= 1 && int(cur) <= len(q.Lineups) {
		q.CurLineup = int(cur)
	}
	p.saveLog(s)
	s.SendExt("QUJING_PTO.S_QUJING_FINISH_SAVE_LINEUP_LIST", nil)
}

func onQjBuyLineup(s *Session, p *Player, _ xym.Msg) {
	q := p.qujing()
	if len(q.Lineups) >= qjLineupMax {
		s.retMsg(fmt.Sprintf("最多只能有%d套阵容。", qjLineupMax))
		return
	}
	s.askBox(fmt.Sprintf("花费#Y%d元宝#n增加一套取经阵容？", qjLineupGold), []string{"购买", "关闭"}, func(opt int) {
		if opt != 0 || !s.spend(0, qjLineupGold) {
			return
		}
		q.Lineups = append(q.Lineups, nil)
		p.saveLog(s)
		s.qjLineups(p)
	})
}

// ---- 援助 ----

func onQjHelp(s *Session, p *Player, _ xym.Msg) {
	q := p.qujing()
	if !q.Active {
		s.retMsg("请先开始取经。")
		return
	}
	text := fmt.Sprintf("招募一名好友的五星护法助战需要花费#Y%d元宝#n（本周第%d次）。#r援助护法最多%d名，满了会替换最早的那一名。",
		qjHelperGold, q.HelpBuys+1, qjHelperMax)
	s.askBox(text, []string{"购买", "关闭"}, func(opt int) {
		if opt != 0 {
			return
		}
		owner, f, card := s.qjPickHelper(p)
		if f == nil {
			s.retMsg("暂时没有可以助战的护法。")
			return
		}
		if !s.spend(0, qjHelperGold) {
			s.retMsg("元宝不足。")
			return
		}
		q.HelpBuys++
		q.HelperSeq++
		h := &qjHelper{BagPos: qjHelperBase + q.HelperSeq, Owner: owner, Fighter: f, HP: int64(f.MaxHP), Card: card}
		if len(q.Helpers) >= qjHelperMax {
			q.Helpers = q.Helpers[1:]
		}
		q.Helpers = append(q.Helpers, h)
		p.saveLog(s)
		s.retMsg(fmt.Sprintf("%s的%s前来助战！（援助页签）", owner, f.Name))
		s.qjSubMain(p)
	})
}

// qjPickHelper 随机挑一名好友（没有好友时别的玩家）出战阵容里的五星护法；都没有时用主流阵容里的一张卡。
func (s *Session) qjPickHelper(p *Player) (string, *battle.Fighter, *qjCard) {
	rng := s.actRng()
	var uids []int32
	if fr := s.srv.social.Friends[p.UID]; len(fr) > 0 {
		uids = slices.Clone(fr)
	} else {
		for _, uid := range s.srv.roles.UIDs() {
			if uid != p.UID {
				uids = append(uids, uid)
			}
		}
	}
	rng.Shuffle(len(uids), func(i, j int) { uids[i], uids[j] = uids[j], uids[i] })
	for _, uid := range uids[:min(len(uids), 20)] {
		hc := s.srv.helperOf(uid)
		if hc == nil || hc.Fighter == nil {
			continue
		}
		pi := gamedata.Pets[hc.Fighter.Icon]
		if pi == nil || pi.Star != 5 {
			continue
		}
		f := qjMemberFighter(gamedata.QujingMember{Type: hc.Fighter.Icon, Talents: hc.Talents, Magic: pi.Magic, MagicLv: 10}, 1.3)
		if f != nil {
			c := hc.Card
			return hc.Name, f, &c
		}
	}
	if pool := gamedata.Qujing.Pvp; len(pool) > 0 {
		t := pool[rng.IntN(len(pool))]
		m := t.Members[rng.IntN(len(t.Members))]
		if f := qjMemberFighter(m, 1.3); f != nil {
			return string(qjNamesA[rng.IntN(len(qjNamesA))]) + qjNamesB[rng.IntN(len(qjNamesB))], f, fullCard(f.Icon)
		}
	}
	return "", nil, nil
}

// ---- 排行 ----

func (s *Session) qjRank(p *Player) {
	q := p.qujing()
	row := func(r *qjRankRow) xym.Msg {
		return xym.Msg{"name": r.Name, "icon": r.Icon, "vip_icon": 0, "stage_hard": r.Best[qjHard][0], "sub_stage_hard": r.Best[qjHard][1],
			"stage_normal": r.Best[qjNormal][0], "sub_stage_normal": r.Best[qjNormal][1], "grade": r.Grade, "xgrade": 0,
			"fight_point": r.FP, "die_pet_count_normal": r.Die[qjNormal], "die_pet_count_hard": r.Die[qjHard],
			"dongfu_icon": 0, "dongfu_name": ""}
	}
	me := &qjRankRow{Name: s.role.Name, Icon: s.role.Icon, Grade: s.role.Grade, Best: q.Best, Die: q.Die}
	msg := row(me)
	msg["server_name"] = "本服"
	var rank []xym.Msg
	for i, r := range s.srv.qjRankList() {
		x := row(r)
		x["pos"] = i + 1
		rank = append(rank, x)
	}
	if rank == nil {
		rank = []xym.Msg{}
	}
	msg["rank"] = rank
	s.SendExt("QUJING_PTO.S_QUJING_OPEN_ALL_RANK", msg)
}

type qjRankRow struct {
	UID   int32          `json:"uid"`
	Name  string         `json:"name"`
	Icon  int            `json:"icon"`
	Grade int            `json:"grade"`
	FP    int            `json:"fp"`
	Best  map[int][2]int `json:"best"`
	Die   map[int]int    `json:"die"`
}

// qjRankScore：困难进度优先，其次普通进度。
func (r *qjRankRow) score() int {
	h, n := r.Best[qjHard], r.Best[qjNormal]
	return (h[0]*10+h[1])*1000 + n[0]*10 + n[1]
}

// qjRankBoard 是本周取经进度榜、优秀试炼录像榜 + 取经人阵容池（全服数据 globalQujing；阵容池跨周保留）。
type qjRankBoard struct {
	Week     int                  `json:"week"`
	Users    map[int32]*qjRankRow `json:"users"`
	Trial    map[int][]qjTrial    `json:"trial,omitempty"` // 劫 → 本周打魂关落宝前三
	Pool     []qjPoolEntry        `json:"pool,omitempty"`
	PoolInit bool                 `json:"pool_init,omitempty"` // 已放进抓包阵容
}

func (srv *Server) qjRankUpdate(p *Player, ro *Role) {
	if ro == nil {
		return
	}
	q := p.qujing()
	srv.qjRankMu.Lock()
	defer srv.qjRankMu.Unlock()
	b := srv.qjRankBoard()
	b.Users[p.UID] = &qjRankRow{UID: p.UID, Name: ro.Name, Icon: ro.Icon, Grade: ro.Grade, FP: prestigeOf(ro, p),
		Best: q.Best, Die: q.Die}
	saveGlobal(srv.players.st, globalQujing, b)
}

func (srv *Server) qjRankList() []*qjRankRow {
	srv.qjRankMu.Lock()
	defer srv.qjRankMu.Unlock()
	b := srv.qjRankBoard()
	out := make([]*qjRankRow, 0, len(b.Users))
	for _, r := range b.Users {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *qjRankRow) int {
		if a.score() != b.score() {
			return b.score() - a.score()
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out[:min(len(out), 50)]
}

// qjRankBoard 返回本周榜（换周清空）；调用方持 qjRankMu。
func (srv *Server) qjRankBoard() *qjRankBoard {
	if srv.qjBoard == nil {
		srv.qjBoard = &qjRankBoard{}
		loadGlobal(srv.players.st, globalQujing, srv.qjBoard)
	}
	if wk := lxWeekOf(lxNow()); srv.qjBoard.Week != wk || srv.qjBoard.Users == nil {
		srv.qjBoard.Week, srv.qjBoard.Users, srv.qjBoard.Trial = wk, map[int32]*qjRankRow{}, nil
	}
	return srv.qjBoard
}

// ---- 优秀试炼录像（地图上的录像按钮：每劫打魂关本周落宝最多的前三名，可看录像）----
//
// C_QUJING_TRIAL_STAGE_RANK{stage} → S_QUJING_OPEN_TRIAL_STAGE_RANK{stage, rank[pos, icon, vip_icon, name, usernum,
// cnt 落宝数, grade]}；C_QUJING_TRIAL_STAGE_RANK_PLAY_RECORD{stage, pos} → 那一场的 S_FIGHT_RECORD（抓包 session-04）。
// 录像单独存（存档库 records 表，kind qujing_trial，key = 劫×4 + 槽位），每劫 4 个槽位轮用：前三名占 3 个，新上榜的写进空着的那个。

const (
	qjTrialTop = 3
)

// qjTrial 是试炼榜的一名：落宝数和录像所在槽位。
type qjTrial struct {
	UID   int32  `json:"uid"`
	Name  string `json:"name"`
	Icon  int    `json:"icon"`
	Grade int    `json:"grade"`
	Cnt   int    `json:"cnt"`
	Slot  int    `json:"slot"`
}

func qjTrialKey(stage, slot int) int64 { return int64(stage*4 + slot) }

// qjTrialOffer：打完第 stage 劫的打魂关，落宝数进本周前三就记下这一场（同一人只留最好的一场）。
func (s *Session) qjTrialOffer(p *Player, stage, cnt int, rec *gateRecord) {
	srv := s.srv
	if cnt <= 0 || s.role == nil {
		return
	}
	srv.qjRankMu.Lock()
	defer srv.qjRankMu.Unlock()
	b := srv.qjRankBoard()
	list := b.Trial[stage]
	if i := slices.IndexFunc(list, func(t qjTrial) bool { return t.UID == p.UID }); i >= 0 {
		if list[i].Cnt >= cnt {
			return
		}
		list = slices.Delete(slices.Clone(list), i, i+1)
	}
	if len(list) >= qjTrialTop && list[len(list)-1].Cnt >= cnt {
		return
	}
	used := map[int]bool{}
	for _, t := range list[:min(len(list), qjTrialTop-1)] {
		used[t.Slot] = true
	}
	slot := 0
	for used[slot] {
		slot++
	}
	if st := srv.players.st; st != nil {
		if err := saveRecord(st, store.RecordQujingTrial, qjTrialKey(stage, slot), rec); err != nil {
			slog.Error("save qujing trial record", "stage", stage, "err", err)
			return
		}
	}
	list = append(list, qjTrial{UID: p.UID, Name: s.role.Name, Icon: s.role.Icon, Grade: s.role.Grade, Cnt: cnt, Slot: slot})
	slices.SortStableFunc(list, func(a, b qjTrial) int { return b.Cnt - a.Cnt })
	if b.Trial == nil {
		b.Trial = map[int][]qjTrial{}
	}
	b.Trial[stage] = list[:min(len(list), qjTrialTop)]
	saveGlobal(srv.players.st, globalQujing, b)
}

func (srv *Server) qjTrialList(stage int) []qjTrial {
	srv.qjRankMu.Lock()
	defer srv.qjRankMu.Unlock()
	return slices.Clone(srv.qjRankBoard().Trial[stage])
}

func onQjTrialRank(s *Session, p *Player, m xym.Msg) {
	stage, _ := m["stage"].(int64)
	rank := []xym.Msg{}
	for i, t := range s.srv.qjTrialList(int(stage)) {
		rank = append(rank, xym.Msg{"pos": i + 1, "icon": t.Icon, "vip_icon": 0, "name": t.Name, "usernum": t.UID,
			"cnt": t.Cnt, "grade": t.Grade})
	}
	s.SendExt("QUJING_PTO.S_QUJING_OPEN_TRIAL_STAGE_RANK", xym.Msg{"stage": stage, "rank": rank})
}

func onQjTrialPlay(s *Session, p *Player, m xym.Msg) {
	stage, _ := m["stage"].(int64)
	pos, _ := m["pos"].(int64)
	list := s.srv.qjTrialList(int(stage))
	if pos < 1 || int(pos) > len(list) {
		s.retMsg("暂无录像。")
		return
	}
	t := list[pos-1]
	r := loadRecord(s.srv.players.st, store.RecordQujingTrial, qjTrialKey(int(stage), t.Slot))
	if r == nil {
		s.retMsg("暂无录像。")
		return
	}
	s.SendExt("FIGHT_PTO.S_FIGHT_RECORD", xym.Msg{
		"canop": 14, "rid": fmt.Sprintf("qujing-%d-trial-%d-8", t.UID, stage),
		"w1": r.W1, "level1": r.Grade, "name1": r.Name, "team1": teamMsgs(r.T1),
		"level2": 1, "name2": r.Name2, "team2": teamMsgs(r.T2),
		"otherparam": recordParams(r.T1, r.T2), "record": r.Record,
	})
}
