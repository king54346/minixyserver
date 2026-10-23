package xymsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// Player 是一个角色的玩法存档（整体存成 JSON，见 store）。同一个 *Player 由 Players 缓存，
// 顶号时新旧会话共用；handler 在 mu 下执行（见 Session.handleExt）。
type Player struct {
	mu sync.Mutex
	st store.Store

	UID          int32           `json:"uid"`
	NextNpc      int32           `json:"next_npc"` // 下一张卡的 npcnum（与官方一样从 10001 起）
	Pets         []*Pet          `json:"pets"`
	FightNo      int             `json:"fno"` // 出战阵容号
	Lineups      []*Lineup       `json:"lineups"`
	Items        []*Item         `json:"items"`
	Prog         [2]*Progress    `json:"progress"` // 闯关进度：[0] 普通 [1] 精英
	Mall         *Mall           `json:"mall,omitempty"`
	Suits        []*Suit         `json:"suits,omitempty"`
	Books        []*Book         `json:"books,omitempty"`
	NextItemNum  int32           `json:"next_itemnum,omitempty"`
	Souls        map[int32]int32 `json:"souls,omitempty"`
	Irons        map[int]int32   `json:"irons,omitempty"`         // 装备碎片：装备类型 -> 数量
	BookParts    map[int32]int32 `json:"book_parts,omitempty"`    // 法术碎片：法术号 -> 数量
	TianJiang    *TianJiangState `json:"tianjiang,omitempty"`     // 天将阶级（tianjiang.go）
	Dangyao      *DangyaoState   `json:"dangyao,omitempty"`       // 荡妖 / 功德（dangyao.go）
	Shilian      *ShilianState   `json:"shilian,omitempty"`       // 试炼（shilian.go）
	Joke         *JokeState      `json:"joke,omitempty"`          // 解忧（jieyou.go）
	Wish         *WishState      `json:"wish,omitempty"`          // 缘魂（wish.go）
	Mijing       *MijingState    `json:"mijing,omitempty"`        // 秘境（mijing.go）
	Tree         *TreeState      `json:"tree,omitempty"`          // 摇钱树（tree.go）
	Duobao       *DuobaoState    `json:"duobao,omitempty"`        // 宝卷 / 夺宝（duobao.go）
	Turntable    *TurntableState `json:"turntable,omitempty"`     // 奇缘幻境（turntable.go）
	Xianling     *XianlingState  `json:"xianling,omitempty"`      // 仙灵店铺（xianling.go）
	Honglu       *HongluState    `json:"honglu,omitempty"`        // 烘炉（alchemy.go）
	Energy       *EnergyState    `json:"energy,omitempty"`        // 体力 / 活力回复与购买（energy.go）
	LineupBuys   int             `json:"lineup_buys,omitempty"`   // 花元宝买的额外阵容数（lineups.go）
	Lianshen     *LianshenState  `json:"lianshen,omitempty"`      // 每日免费炼神（inlay.go）
	SweepTickets int             `json:"sweep_tickets,omitempty"` // 扫荡券（sweep.go）
	GoldSpent    int64           `json:"gold_spent,omitempty"`    // 还没凑满 100 的已花元宝
	Yuanshen     int             `json:"yuanshen,omitempty"`      // 元神（huaxing.go）
	HuaxingTimes map[int32]int   `json:"huaxing_times,omitempty"` // 各护法已化形次数
	AutoYuanshen []int32         `json:"auto_yuanshen,omitempty"` // 勾选了自动转元神的五星护法
	Tasks        *TaskState      `json:"tasks,omitempty"`         // 日常活跃度 / 主线任务（tasks.go）
	Lundao       *LundaoState    `json:"lundao,omitempty"`        // 论道积分 / 次数 / 兑换（lundao.go）
	Jiuyou       *JiuyouState    `json:"jiuyou,omitempty"`        // 九幽试炼次数 / 商店（jiuyou.go）
	Xiuxing      *XiuxingState   `json:"xiuxing,omitempty"`       // 修行（xiuxing.go）
	Zhenfa       *ZhenfaState    `json:"zhenfa,omitempty"`        // 阵法（zhenfa.go）
	DF           *DFPlayer       `json:"df,omitempty"`            // 洞府个人修为 / 灵气 / 建设任务（dongfu_build.go）
	BagLimit     [4]int          `json:"bag_limit"`
	BagBuys      [4]int          `json:"bag_buys,omitempty"`    // 装备 / 法术背包已花元宝扩充的次数（价格 ×3 递增）
	HelperUsed   map[int32]int64 `json:"helper_used,omitempty"` // 援助：借用过的玩家 -> 借用时刻（30 分钟冷却，helper.go）

	role *Role // 进游戏时设置（人物等级等；不存档）
}

// grade 是人物等级（未进游戏时为 0）。
func (p *Player) grade() int {
	if p.role == nil {
		return 0
	}
	return p.role.Grade
}

// Progress 是一种模式（普通 / 精英）的闯关进度。任务号 = 地图号*100 + 序号，按数值递增解锁；
// 一个任务的关卡依次打通后任务通关。S_SEND_MAP_2 的 curr_task 是最后通关的任务，info 是已通关
// 任务及星数（抓包：curr_task 1101 时 info 含 1101）。
type Progress struct {
	Last  int         `json:"last"`  // 最后通关的任务，0 = 还没有
	Gates map[int]int `json:"gates"` // 关卡号 -> 通关次数
	Stars map[int]int `json:"stars"` // 任务号 -> 星数（通关即 1 星）
	// 今日挑战次数（S_GATE_LIST / S_GATE_INFO 的 times，界面「挑战次数 n / 每日上限」；抓包里已通关的关 times 常为 0）
	Day   string      `json:"day,omitempty"`
	Daily map[int]int `json:"daily,omitempty"`
	// GateStars 是成就挑战后的关卡星数（2 / 3；没有记录按通关 = 1，gate_star.go gateStar）
	GateStars map[int]int `json:"gate_stars,omitempty"`
}

// today 是关卡 gid 今天打过（赢 / 扫荡）的次数。
func (pr *Progress) today(gid int) int {
	if pr.Day != today() {
		return 0
	}
	return pr.Daily[gid]
}

func newProgress() *Progress { return &Progress{Gates: map[int]int{}, Stars: map[int]int{}} }

// nextTask 返回 last 之后的任务（本图打完进下一张图的第 1 个任务）；全部打完返回 0。
func nextTask(last int) int {
	if last == 0 {
		return 101
	}
	m, i := last/100, last%100
	if i < gamedata.MapTasks[m] {
		return last + 1
	}
	if gamedata.MapTasks[m+1] > 0 {
		return (m+1)*100 + 1
	}
	return 0
}

// taskOpen：任务已通关或是下一个要打的；精英任务还要求同号普通任务已通关。
func (p *Player) taskOpen(typ, task int) bool {
	pr := p.Prog[typ]
	if pr.Stars[task] > 0 {
		return true
	}
	if task != nextTask(pr.Last) {
		return false
	}
	return typ == 0 || p.Prog[0].Stars[task] > 0
}

// gateOpen：任务开放，且是本任务第一关或上一关已打过。
func (p *Player) gateOpen(typ, gid int) bool {
	g := gamedata.Gates[gid]
	if g == nil || !p.taskOpen(typ, g.Task) {
		return false
	}
	l := gamedata.TaskGates[g.Task]
	for i, id := range l {
		if id == gid {
			return i == 0 || p.Prog[typ].Gates[l[i-1]] > 0
		}
	}
	return false
}

// clearGate 记一次通关；整个任务的关卡都打过时任务通关，返回 true。
func (p *Player) clearGate(typ, gid int) (taskDone bool) {
	pr, task := p.Prog[typ], gamedata.Gates[gid].Task
	pr.Gates[gid]++
	if d := today(); pr.Day != d || pr.Daily == nil {
		pr.Day, pr.Daily = d, map[int]int{}
	}
	pr.Daily[gid]++
	if pr.Stars[task] > 0 {
		return false
	}
	for _, id := range gamedata.TaskGates[task] {
		if pr.Gates[id] == 0 {
			return false
		}
	}
	pr.Stars[task] = 1
	if task > pr.Last {
		pr.Last = task
	}
	return true
}

type Pet struct {
	NpcNum    int32  `json:"npcnum"`
	Type      int32  `json:"type"`
	Grade     int    `json:"grade"`
	Exp       int32  `json:"exp"`
	BagPos    int    `json:"bag_pos"`
	SoulGrade int    `json:"soul_grade,omitempty"`
	Locked    bool   `json:"locked,omitempty"`
	TalGrade  [3]int `json:"tlgrade,omitempty"`     // 三个神通槽的等级，0 按 1 算（S_UPDATE_PET tlgrade1-3）
	MagicLv   int    `json:"magic_lv,omitempty"`    // 天生法术等级，0 按 1 算（tal_magic_lev）
	MagicExp  int32  `json:"magic_exp,omitempty"`   // 天生法术本级经验（tal_magic_exp）
	TLPoint   int    `json:"tlpoint,omitempty"`     // 可用神通点（境界提升、并入同名卡获得，升神通消耗；pet_talent.go）
	TLAdd     int    `json:"tlpoint_add,omitempty"` // 已并入的同名卡张数（S_UPDATE_PET.tlpoint_add）
	ExtOn     bool   `json:"ext_on,omitempty"`      // 第四神通已激活（disishentong.go）
	ExtGrade  [3]int `json:"ext_grade,omitempty"`   // 第四神通三个候选的等级
	ExtIdx    int    `json:"ext_idx,omitempty"`     // 选中的候选（1-3，0 未选）
	AttAdd    int    `json:"attadd,omitempty"`      // 培养：吃过的战力丹次数，每次 +1 攻击（pet_point.go）
	HPAdd     int    `json:"hpadd,omitempty"`       // 培养：吃过的气血丹次数，每次 +4 生命
	// 修行结缘 / 同修（xiuxing.go）：Combine 是结缘得到的人物缘（护法的 extend_relate），Together 是其中已同修进阶的
	Combine  []int32 `json:"combine_relate,omitempty"`
	Together []int32 `json:"together_relate,omitempty"`
}

// addExp 加卡牌经验并升级（exp 是本级内进度，升级时扣掉本级所需，抓包核对过）。
// 等级不超过 limit（人物等级×2）；到上限后经验照加，人物升级后再加经验时补升。返回升了几级。
func (pet *Pet) addExp(n int32, limit int) int {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return 0
	}
	pet.Exp += n
	up := 0
	for need := gamedata.CardNeed(pi.Star, pet.Grade); need > 0 && pet.Grade < limit && pet.Exp >= need; need = gamedata.CardNeed(pi.Star, pet.Grade) {
		pet.Exp -= need
		pet.Grade++
		up++
	}
	return up
}

// petStats 是卡牌的攻击 / 生命：等级公式 + 出战位所穿装备（服务端下发的是叠加后的值）。
func (p *Player) petStats(pet *Pet) (att, hp int32) {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return 0, 0
	}
	att, hp = pi.Att(pet.Grade)+int32(pet.AttAdd), pi.HP(pet.Grade)+int32(pet.HPAdd*pointHPStep) // 培养加在缘分之前（pet_prop.lua）
	if fp := p.fightPos(pet.BagPos); fp > 0 {
		ga, gh := p.gearBonus(fp)
		att = int32(float64(att+ga) * p.relateRatio(pi, fp, false) * p.extendRatio(pet, false)) // 缘分（relate.go）
		hp = int32(float64(hp+gh) * p.relateRatio(pi, fp, true) * p.extendRatio(pet, true))
	}
	return att, hp
}

// wornBooks 是出战位 fp 穿着的法术书 {法术号, 等级}。
func (p *Player) wornBooks(fp int) [][2]int32 {
	var out [][2]int32
	if sl := p.fightSlot(fp); sl != nil {
		for _, pos := range sl.Book {
			if b := p.bookByPos(pos); b != nil {
				out = append(out, [2]int32{b.Type, int32(b.Grade)})
			}
		}
	}
	return out
}

// wornSuits 是出战位 fp 穿着的装备 {装备位, 装备号, 强化等级, 天将阶}（战报详情用）。
func (p *Player) wornSuits(fp int) [][5]int32 {
	var out [][5]int32
	if sl := p.fightSlot(fp); sl != nil {
		for i, pos := range sl.Suit {
			if s := p.suitByPos(pos); s != nil {
				out = append(out, [5]int32{int32(i + 1), int32(s.Type), int32(s.Grade), int32(p.suitTJ(s.Type)), int32(s.Stone)})
			}
		}
	}
	return out
}

// wornBookTJ 与 wornBooks 按下标对应：法术书的天将阶。
func (p *Player) wornBookTJ(fp int) []int {
	var out []int
	if sl := p.fightSlot(fp); sl != nil {
		for _, pos := range sl.Book {
			if b := p.bookByPos(pos); b != nil {
				out = append(out, p.magicTJ(b.Type))
			}
		}
	}
	return out
}

func (pet *Pet) talGrade(i int) int { return max(pet.TalGrade[i], 1) }

// shownTalGrade 是发给客户端的 tlgrade：境界还没开的槽发 0（官方抓包：境界 0 的卡 tlgrade 全是 0，界面显示 0/N）。
func (pet *Pet) shownTalGrade(i int) int {
	if i >= talentSlots(pet.SoulGrade) {
		return 0
	}
	return pet.talGrade(i)
}
func (pet *Pet) magicLv() int { return max(pet.MagicLv, 1) }

// Lineup 一个阵容：8 个位置，Slot.Pet 存卡牌的 bag_pos（0 = 空），与 S_SEND_LINEUP 一致。
type Lineup struct {
	No     int      `json:"no"`
	Leader int      `json:"leader"` // 队长所在位置
	Slots  [8]*Slot `json:"slots"`
}

type Slot struct {
	Pet  int    `json:"pet"`
	Suit [3]int `json:"suit,omitempty"` // spos1-3
	Book [2]int `json:"book,omitempty"` // mpos1-2
}

type Item struct {
	Pos    int   `json:"pos"`
	Type   int   `json:"type"`
	Amount int32 `json:"amount"`
	Expire int32 `json:"expire,omitempty"`
}

const (
	firstNpcNum    = 10001
	defaultBagSize = 100
	lineupSlots    = 8
)

// starterPets 是建角送的卡，按官方测试号最早拿到的 5 张（npcnum 10001-10007，新手剧情所得）。
var starterPets = []int32{13010, 33001, 32002, 34001, 33002}

func newPlayer(uid int32) *Player {
	p := &Player{UID: uid, NextNpc: firstNpcNum, FightNo: 1, Prog: [2]*Progress{newProgress(), newProgress()}}
	for i := 1; i < len(p.BagLimit); i++ {
		p.BagLimit[i] = defaultBagSize
	}
	lu := &Lineup{No: 1, Leader: 1}
	for i := range lu.Slots {
		lu.Slots[i] = &Slot{}
	}
	p.Lineups = []*Lineup{lu}
	for i, t := range starterPets {
		pet := p.addPet(t)
		lu.Slots[i].Pet = pet.BagPos
	}
	return p
}

// addPet 发一张 1 级卡，放进第一个空的背包位。
func (p *Player) addPet(typ int32) *Pet {
	used := map[int]bool{}
	for _, pt := range p.Pets {
		used[pt.BagPos] = true
	}
	pos := 1
	for used[pos] {
		pos++
	}
	pet := &Pet{NpcNum: p.NextNpc, Type: typ, Grade: 1, BagPos: pos}
	p.NextNpc++
	p.Pets = append(p.Pets, pet)
	return pet
}

func (p *Player) lineup(no int) *Lineup {
	for _, lu := range p.Lineups {
		if lu.No == no {
			return lu
		}
	}
	return nil
}

// fightPos 返回卡牌在出战阵容里的位置（1-8），不在阵上为 0。
func (p *Player) fightPos(bagPos int) int {
	if lu := p.lineup(p.FightNo); lu != nil {
		for i, s := range lu.Slots {
			if s != nil && s.Pet == bagPos {
				return i + 1
			}
		}
	}
	return 0
}

// ---- 协议编码 ----

// petSpeed 是卡牌的速度（先攻）：基础值 + 出战位所穿战靴的先攻（suit.csv speed）+ 5 星战靴的天将加成。
func (p *Player) petSpeed(pet *Pet) int {
	pi := gamedata.Pets[pet.Type]
	if pi == nil {
		return 0
	}
	if fp := p.fightPos(pet.BagPos); fp > 0 {
		return pi.Speed + p.shoeSpeed(fp) + p.tjSpeed(fp)
	}
	return pi.Speed
}

// shoeSpeed 是出战位 fp 所穿战靴加的先攻。
func (p *Player) shoeSpeed(fp int) int {
	sl := p.fightSlot(fp)
	if sl == nil {
		return 0
	}
	if s := p.suitByPos(sl.Suit[2]); s != nil {
		if si := gamedata.Suits[s.Type]; si != nil && si.Kind == "ITK_SHOE" {
			return si.Speed
		}
	}
	return 0
}

func (p *Player) petFull(pet *Pet) xym.Msg {
	m := xym.Msg{"npcnum": pet.NpcNum, "fight_pos": p.fightPos(pet.BagPos), "bag_pos": pet.BagPos,
		"type": pet.Type, "grade": pet.Grade, "exp": pet.Exp, "soul_grade": pet.SoulGrade,
		"tal_magic_lev": pet.magicLv(), "tal_magic_exp": pet.MagicExp, "tlgrade1": pet.shownTalGrade(0), "tlgrade2": pet.shownTalGrade(1),
		"tlgrade3": pet.shownTalGrade(2), "is_lock": pet.Locked, "tlpoint": pet.TLPoint, "tlpoint_add": pet.TLAdd,
		"hpadd": pet.HPAdd, "attadd": pet.AttAdd, "combine_relate": ridList(pet.Combine), "together_relate": ridList(pet.Together)}
	tj := p.petTJ(pet)
	m["tlxlv1"], m["tlxlv2"], m["tlxlv3"] = tj[0], tj[1], tj[2]
	m["extlgrade1"], m["extlgrade2"], m["extlgrade3"] = pet.ExtGrade[0], pet.ExtGrade[1], pet.ExtGrade[2]
	m["extlidx"], m["extlstate"] = pet.ExtIdx, p.extlState(pet)
	if pi := gamedata.Pets[pet.Type]; pi != nil {
		att, hp := p.petStats(pet)
		m["att"], m["hp"], m["speed"] = att, hp, p.petSpeed(pet)
	}
	return m
}

func (p *Player) petBrief(pet *Pet) xym.Msg {
	return xym.Msg{"fight_pos": p.fightPos(pet.BagPos), "bag_pos": pet.BagPos, "npcnum": pet.NpcNum,
		"grade": pet.Grade, "exp": pet.Exp, "soul_grade": pet.SoulGrade, "type": pet.Type,
		"tlgrade1": pet.shownTalGrade(0), "tlgrade2": pet.shownTalGrade(1), "tlgrade3": pet.shownTalGrade(2), "is_lock": pet.Locked,
		"tlpoint_add": pet.TLAdd}
}

func (p *Player) sortedPets() []*Pet {
	pets := append([]*Pet(nil), p.Pets...)
	sort.Slice(pets, func(i, j int) bool { return pets[i].BagPos < pets[j].BagPos })
	return pets
}

func (p *Player) lineupsMsg() xym.Msg {
	p.ensureLineups()
	var lus []xym.Msg
	for _, lu := range p.Lineups {
		var info []xym.Msg
		for i, s := range lu.Slots {
			if s == nil {
				s = &Slot{}
			}
			info = append(info, xym.Msg{"pos": i + 1, "pet": s.Pet,
				"spos1": s.Suit[0], "spos2": s.Suit[1], "spos3": s.Suit[2], "mpos1": s.Book[0], "mpos2": s.Book[1]})
		}
		lus = append(lus, xym.Msg{"lineup_no": lu.No, "leader": lu.Leader, "info": info})
	}
	// count = 已开的阵容数（抓包 5 套阵容时 count 5；客户端 multiteam.getOpenCount），见 lineups.go
	return xym.Msg{"fno": p.FightNo, "lineups": lus, "count": p.lineupCount()}
}

// ---- 存取 ----

// Players 缓存并持久化所有在线过的玩家存档。
type Players struct {
	st    store.Store
	mu    sync.Mutex
	byUID map[int32]*Player
}

func OpenPlayers(st store.Store) *Players { return &Players{st: st, byUID: map[int32]*Player{}} }

// Get 读档；没有存档时建一份新手档并落盘。
func (ps *Players) Get(uid int32) (*Player, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if p := ps.byUID[uid]; p != nil {
		return p, nil
	}
	data, err := ps.st.LoadPlayer(uid)
	var p *Player
	switch {
	case errors.Is(err, store.ErrNotFound):
		p = newPlayer(uid)
		p.st = ps.st
		if err := p.save(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		p = &Player{}
		if err := json.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("player %d: %w", uid, err)
		}
		p.st = ps.st
		for i := range p.Prog {
			if p.Prog[i] == nil {
				p.Prog[i] = newProgress()
			}
			if p.Prog[i].Gates == nil {
				p.Prog[i].Gates = map[int]int{}
			}
			if p.Prog[i].Stars == nil {
				p.Prog[i].Stars = map[int]int{}
			}
		}
	}
	fixed := false
	for _, su := range p.Suits { // 20 级以上还没有炼神效果的补一个；旧存档的高级炼神效果归成 1 级（inlay.go）
		fixed = su.ensureSkill() || fixed
		fixed = su.normalizeSkills() || fixed
	}
	fixed = p.fixAllGear() || fixed   // 阵容里错位 / 失效的装备、法术书引用（bag.go）
	if p.mergeDuplicates() || fixed { // 旧存档里的同名重复卡并进同类（官方不留重复卡，pet_talent.go）
		if err := p.save(); err != nil {
			return nil, err
		}
	}
	ps.byUID[uid] = p
	return p, nil
}

// save 写存档；调用方持有 p.mu（或 p 尚未共享）。
func (p *Player) save() error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return p.st.SavePlayer(p.UID, data)
}

// saveLog 存档失败只记日志：玩法继续，下次改动会再写。
func (p *Player) saveLog(s *Session) {
	if err := p.save(); err != nil {
		s.logErr("save player", err)
	}
}
