package battle

import (
	"math/rand/v2"
	"sort"
)

// 战斗由服务端算完，以命令流（S_FIGHT_RECORD.record）下发，客户端只播放。命令格式照
// client war/netwar.lua 的 C*:parseData，解码器见 analysis/tools/fight_record.py。
//
// 流程照官方战报：WAR_START → 每回合 [TURN、后阵补位、回合开始神通、按速度依次行动、
// ACTION_END、双方战意 +10、回合结束神通] → WAR_END。前阵 1-5 位出手，6-8 位是后阵，
// 前阵阵亡后下回合开始时依次补位。每次行动：法术（满足概率/次数/战意时，在普攻之前额外
// 施放）→ 攻击前神通 → 普攻（闪避、暴击、减伤、受击神通）→ 攻击后神通。
const (
	CmdActDo        = 3
	CmdSubHP        = 4
	CmdAddHP        = 5
	CmdDie          = 6
	CmdRelive       = 7
	CmdBuff         = 8
	CmdTurn         = 9
	CmdMove         = 10
	CmdWarStart     = 11
	CmdWarEnd       = 12
	CmdZhanyi       = 13
	CmdAttChange    = 14
	CmdRmBuff       = 17
	CmdShield       = 18
	CmdChgMaxHP     = 19
	CmdAddPower     = 20 // 蓄力层数（op 1 加 2 减）
	CmdChgFighter   = 26 // 变身：pos, 形象, 生命, 攻击, 先攻, full, 种族, power（归元火之卵）
	CmdBloodPoolIn  = 33 // 血池蓄入：pos, 量（燃魂）
	CmdBloodPoolAtt = 34 // 血池攻击：目标 pos, 量
	CmdDitingCount  = 41 // 谛听计数 -1：pos（客户端头像上的返璞归真剩余次数，入阵时重置为 10）
	CmdXiuluoCount  = 42 // 修罗点数：pos, 点数（阿修罗 / 天女魃头像上的计数）
	CmdActionEnd    = 22
	CmdMagicShield  = 23
	CmdPinXue       = 27 // 回合用完拼血（无参数）
	CmdMoveToEmpty  = 36 // 后阵补到从没人站过的空位（客户端 moveToEmpty 会重排站位表；用 MOVE 会因空位不在表里报错）

	// war/config.lua ATTACKTYPE_*
	attackNormal = 1
	attackCrit   = 2
	attackDodge  = 3
	attackGuard  = 4 // ATTACKTYPE_PROTECT：护卫演出

	maxRounds  = 10  // 官方战报：所有玩法最多 10 回合（session-01/02 共 1100+ 场）
	enemyBase  = 100 // 敌方站位 = 100 + 槽位（抓包里为 101-109）
	FightSlots = 9
	frontSlots = 5 // 1-5 前阵，其余后阵
	zyPerRound = 10
	maxZY      = 100 // 战意上限
)

type battle struct {
	units      [2][]*unit
	zf         [2]*zfSide // 双方阵法（zhenfa.go；nil = 不用）
	prestige   [2]int     // 双方威望（同速时威望高的先出手）
	zy         [2]int
	rng        *rand.Rand
	inTempUp   bool  // tempUp 进行中（attRaised 用）
	fixedHit   bool  // 下一次 hurt 是固定伤害（守卫的 1 点）：不经队长技 / 阵法 / 会猎缩放
	lastVictim *unit // 最近一次 attack 实际承受攻击的单位（守卫 / 元形代挨时是护卫者），攻击特效作用于它
	atkDepth   int   // 攻击行为的嵌套层数（attack_action.go）
	enterDepth int   // 换位触发入阵的嵌套层数（swap）
	w          recordWriter
	skills     map[string]int
	round      int
	actor      *unit    // 最近一次演出的单位（回复效果类队长技按它算施治者）
	pierce     bool     // 本次 hurt 是生命流失：不经结界（loseHP 设置）
	lastDealt  int64    // 上一次 hurt 的伤害值（结界抵消后、未按剩余血量截断；吸血 / 血气按它算）
	pool       [2]int64 // 血池（燃魂失去的生命）
	poolBy     [2]*unit // 最近蓄入血池的单位与其燃魂号（演出用）
	poolID     [2]int32
	over       bool
	win        bool
}

// Result 是一场战斗的结果。
type Result struct {
	Record []byte
	// ZfParam 是战报 S_FIGHT_RECORD.otherparam idx 11 的值（双方阵法，客户端显示用；没人用阵法时为空）
	ZfParam string
	Win     bool
	Rounds  int
	Skills  map[string]int // 触发的神通 / 法术（按名字计数，日志用）
}

// 九幽首领的护甲神通（auto/talent.lua：130234 魔甲、130235 藤甲）。
const (
	talentBossArmor = 130234
	talentVineArmor = 130235
	bossHitCap      = 10
)

// Simulate 打一场；rng 为 nil 时用随机种子。
func Simulate(team1, team2 []*Fighter, rng *rand.Rand) Result {
	return SimulateLeaders(team1, team2, [2]int32{}, rng)
}

// SimulateLeaders 同 Simulate，leaders 是双方的队长技号（0 = 无，见 leader.go）。
func SimulateLeaders(team1, team2 []*Fighter, leaders [2]int32, rng *rand.Rand) Result {
	return SimulateWith(team1, team2, Options{Leaders: leaders}, rng)
}

// Options 是一场战斗的双方设置。
type Options struct {
	WarType  int      // WAR_START 的战斗类型（war/netwar.lua：0 普通、1 九幽试炼——客户端显示首领伤害面板）
	Leaders  [2]int32 // 队长技号（0 = 无）
	Assist   [2]int32 // 援助护法（好友 / 路人的队长）的队长技，与本方队长技同时生效（0 = 无）
	Prestige [2]int   // 威望（S_FIGHT_RECORD.w1 / w2）：同速时威望高的一方先出手
	// Formations 是双方阵法（zhenfa.go；Type 0 = 不用）
	Formations [2]Formation
}

// SimulateWith 按 opts 打一场。
func SimulateWith(team1, team2 []*Fighter, opts Options, rng *rand.Rand) Result {
	leaders := opts.Leaders
	if rng == nil {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	b := &battle{rng: rng, skills: map[string]int{}, prestige: opts.Prestige}
	for side, team := range [2][]*Fighter{team1, team2} {
		for _, f := range team {
			u := newUnit(f, side)
			u.lead = leaderBonusFor(leaders[side], f.Race).plus(leaderBonusFor(opts.Assist[side], f.Race))
			u.lead.mdmg += f.MagicUp
			u.lead.healout += f.HealUp
			u.lead.ndmg += f.Gems[0]
			u.lead.mdmg += f.Gems[1]
			u.lead.nres += f.Gems[2]
			u.lead.mres += f.Gems[3]
			u.lead.healin += f.Gems[4]
			u.lead.healout += f.Gems[5]
			b.units[side] = append(b.units[side], u)
		}
	}
	b.w.u8(CmdWarStart)
	b.w.u8(opts.WarType)
	b.setupZhenfa(opts.Formations)
	b.battleStart()
	for side := range b.units {
		for _, u := range b.units[side] {
			b.initPellets(u) // 火炮之术：开场前后阵都装弹（开场不触发入阵神通，见 enter）
		}
	}
	b.afterOpening()
	for b.round = 1; b.round <= maxRounds && !b.over; b.round++ {
		b.playRound()
	}
	if !b.over {
		b.w.u8(CmdPinXue)                   // 回合用完：客户端播「拼血」（war/netwar.lua CPinXue）
		b.finish(b.hpLeft(0) > b.hpLeft(1)) // 剩余总生命多的一方胜
	}
	return Result{Record: b.w.b, Win: b.win, Rounds: min(b.round, maxRounds), Skills: b.skills, ZfParam: zfParam(opts.Formations)}
}

func (b *battle) playRound() {
	b.w.u8(CmdTurn)
	b.roundReset()
	for side := range b.units {
		b.moveUp(side)
	}
	for _, u := range b.order() {
		u.acted = false
		b.roundStart(u)
	}
	for side := range b.units { // 布剑：回合开始召剑可在后阵
		for _, u := range b.units[side] {
			if u.alive() && !u.front() && !u.gone {
				if k := u.tal(famBuJian); k != nil && b.chance(k.param(0)) {
					b.addSword(u, k)
				}
			}
		}
	}
	if b.checkOver() {
		return
	}
	for _, u := range b.order() {
		if !u.alive() || !u.front() || u.acted {
			continue
		}
		u.acted = true
		b.act(u)
		if b.checkOver() {
			return
		}
	}
	b.w.u8(CmdActionEnd)
	for side := range b.units {
		b.addZY(side, zyPerRound)
	}
	for _, u := range b.order() {
		b.roundEnd(u)
	}
	b.zfEyeSkill() // 阵眼技：指定回合结束时（zhenfa.go）
	// 回合制状态到期统一放在所有回合结束神通之后：回合结束时才获得的「到当前回合结束」状态（如飞仙遁入换上的
	// 光阴碎给的无敌）也在本回合结束时消失，不再因出手顺序先后有的多留一回合；后阵护法的状态同样计时。
	for side := range b.units {
		for _, u := range b.units[side] {
			if u.alive() {
				b.tickStatus(u, false)
			}
		}
	}
	b.roundOver()
	b.checkOver()
}

// order 是本回合前阵存活单位的出手顺序：按列从左到右（槽位 1→5），同一列敌我两人速度快的先；
// 同速威望高的一方先，威望相同我方先。官方战报（session-01/02）第 1 回合 1030 场里 1007 场完全符合
// （全场按速度排序只符合 24 场）；同速对位 26 次里 19 次威望高的先，其余是开场神通改过速度。
func (b *battle) order() []*unit {
	var all []*unit
	for _, us := range b.units {
		for _, u := range us {
			if u.alive() && u.front() {
				all = append(all, u)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		x, y := all[i], all[j]
		if x.slot() != y.slot() {
			return x.slot() < y.slot()
		}
		if sx, sy := x.speed(), y.speed(); sx != sy {
			return sx > sy
		}
		return b.firstSide() == x.side
	})
	return all
}

// firstSide 是同速时先出手的一方：威望高的，相同则我方。
func (b *battle) firstSide() int {
	if b.prestige[1] > b.prestige[0] {
		return 1
	}
	return 0
}

// moveUp：前阵空位由后阵存活单位按槽位顺序补上（抓包：106→101、107→102…）。
func (b *battle) moveUp(side int) {
	occupied := map[int]bool{}
	dead := map[int]*unit{}
	var bench []*unit
	for _, u := range b.units[side] {
		if u.alive() && u.front() {
			occupied[u.slot()] = true
		}
		// 死者按实际站位算（被锢魂 / 吞噬标记离场的 gone 也还摆在客户端这个槽上）：否则会发 MOVETO_EMPTY，
		// 客户端在死卡上再叠一张卡，死卡也下不去。
		if !u.alive() && u.slot() <= frontSlots {
			dead[u.slot()] = u
		}
		if u.alive() && !u.front() {
			bench = append(bench, u)
		}
	}
	sort.Slice(bench, func(i, j int) bool { return bench[i].slot() < bench[j].slot() })
	for s := 1; s <= frontSlots && len(bench) > 0; s++ {
		if occupied[s] {
			continue
		}
		u := bench[0]
		bench = bench[1:]
		if d := dead[s]; d != nil {
			d.gone = true
		}
		to := s + side*enemyBase
		from := u.Pos
		if dead[s] != nil {
			b.w.u8(CmdMove)
			dead[s].Pos = from // 客户端 MOVE 是交换：死卡到后阵
		} else {
			b.w.u8(CmdMoveToEmpty)
		}
		b.w.u8(from)
		b.w.u8(to)
		u.Pos = to
		b.zfSync(u) // 阵法：补上来的护法取得该阵位的固定加成
		b.enter(u)
		// enter 中的入阵神通（如甘露）可能复活了某槽死者，同步空位表，避免同槽两人。
		for _, x := range b.units[side] {
			if x.alive() && x.front() {
				occupied[x.slot()] = true
			}
		}
	}
}

func (b *battle) checkOver() bool {
	if b.over {
		return true
	}
	alive := [2]bool{}
	for side, us := range b.units {
		for _, u := range us {
			if u.alive() {
				alive[side] = true
			}
		}
	}
	if !alive[0] || !alive[1] {
		b.finish(alive[0])
	}
	return b.over
}

// hpLeft 是一方全部存活单位（含后阵）的当前生命之和。
func (b *battle) hpLeft(side int) int64 {
	var n int64
	for _, u := range b.units[side] {
		if u.alive() && !u.gone {
			n += u.hp
		}
	}
	return n
}

func (b *battle) finish(win bool) {
	b.over, b.win = true, win
	b.w.u8(CmdWarEnd)
	b.w.u8(b2i(win))
}

func (b *battle) act(u *unit) {
	defer func() {
		if u.alive() {
			b.tickStatus(u, true)
		} // 行动中阵亡的状态已在 died → leaveClear 里清掉（锢魂保留）
	}()
	if u.has(stBind) != nil {
		return
	}
	if u.egg { // 火之卵：每次行动必放火系群法，不能普攻（归元）
		b.eggFire(u, eggMagic)
		return
	}
	if u.baigu && b.round%2 == 1 && len(u.magics) > 0 {
		b.cast(u, u.magics[0]) // 白骨魔：奇数回合必放疯魔乱棍
	} else if u.has(stSilence) == nil {
		b.castMagic(u)
	}
	if !u.alive() || b.over {
		return
	}
	if k := chargeTalent(u); k != nil && chargeNeed(k) > 0 && u.charge >= chargeNeed(k) {
		b.releaseCharge(u, k)
		if !u.alive() || b.over {
			return
		}
	}
	t := b.pickTarget(u)
	if s := u.has(stTaunt); s != nil && s.src != nil && s.src.alive() && s.src.front() && s.src.has(stHidden) == nil {
		t = s.src // 嘲讽：攻击嘲讽的来源
	}
	if u.has(stConfuse) != nil {
		var mates []*unit
		for _, x := range b.allies(u) {
			if x != u {
				mates = append(mates, x)
			}
		}
		if len(mates) > 0 {
			t = mates[b.rng.IntN(len(mates))]
		}
	} else if k := u.tal(famChongFeng); k != nil && t != nil && b.chance(k.param(0)) {
		low := t
		for _, x := range b.attackable(u) { // 易死目标：生命最少、不无敌、不隐身
			if !b.invincible(x) && x.hp < low.hp {
				low = x
			}
		}
		dist := int64(abs(low.slot()-u.slot()) + 1) // 对位为 1
		t = low
		b.tempUp(u, k, u.baseAtt*int64(k.param(1))*dist/100)
	}
	if t == nil {
		return
	}
	b.attackAction(u, t, 100, atkMain) // attack_action.go
}
