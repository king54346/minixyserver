package xymsrv

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 试炼（无尽战斗）的规则。协议见 h_shilian.go。
//
// 依据（客户端）：logic/battle_mgr.lua（状态 MAIN/ADD/CHOICE/BOSS、加成类型 1 攻击 2 血量 3 魔法 4 治疗、
// 三档加成各耗 3 / 15 / 30 星）、view/BattleView.lua（三个对手 死战 / 力战 / 战斗 的得星倍数 = 4 − idx、
// 只有死战带「遗落的财宝」、财宝每过 5 关结算一次、fight2face 为 0 时是魔王关、今日剩余次数、
// 「今日最佳」与「预测今日排名」）、view/BattleFailEntranceView.lua（「N 关开始」走 C_RESTART_LAST）、
// config.lua getPlayerGroup（排行按等级分 ≤30 / ≤40 / ≤50 三组，C_OPEN_RANK.type = 30/40/50）。
//
// 依据（官方抓包 session-02，两轮 501 关共 968 场死战，全部核对过）：
//   - 敌方是真实卡牌：等级 L = min(2 × 关数 − 1, 200)（S_FIGHT_RECORD.level2），
//     攻击 / 血量 = 卡牌 L 级的基础值 × (1 + 0.08 × (关数 − 1))，速度不变（每关每张卡比值误差 < 0.1%）；
//     S_SEND_NPC_TEAM 里法术 10 级、每个神通槽取最高级；
//   - 敌方人数 = min(9, 5 + 关数 / 4)，力战 / 战斗依次少 1 人（fightNcount2：5/4/3 … 9/9/8 … 9/9/9）；
//   - 每 10 关是魔王关（只有死战一个对手）；
//   - 死战每关 9 星（= 3 × 3）、银两 300 × 关数（每关单独发 S_ATTR_UPDATE），一轮结束 S_REWARD_MSG{type 13}
//     汇总本轮银两（501 关正好 300 × 501 × 502 / 2）；
//   - 每过 5 关：财宝结算成物品 10043 × 财宝数（S_REWARD_MSG{type 1}），再 S_SEND_ADD 给 3 个不同类型的加成，
//     数值固定 6 / 30 / 60；魔王关的结算有时多一张魔王卡的魂魄（100 个魔王关里 14 次）；
//   - 我方攻击 / 血量 = 原值 × (1 + 加成%) 取整（瑶姬仙子 2051 → 加成 60% 时 3281）；
//   - 第 501 关打完一轮结束（maxstage 501），last_stage 记 500；每日 3 次（S_SEND_STATE.times 2 → 1 → 0）；
//   - 历史最好成绩够高时 is_con = 1，C_CONTINUOUS_FIGHT 自动打到下一个 5 的倍数关（不发战报）。
//
// 估计（没有直接证据，待抓包）：
//   - 力战 / 战斗的银两按得星倍数折算（100 × 关数 × (4 − idx)）；
//   - 敌方星级：上限 min(5, 1 + 关数 / 6)（抓包里 1–5 关全是 1 星、6 关起出 2 星、12 关 3 星、18 关 4 星、
//     24 关 5 星），前两位取上限，其余偏向高星随机；魔王关首位是随机 5 星卡；法术等级 min(10, 1 + (关数 − 1) / 20)；
//   - 魔王魂魄概率 15%；输掉时没结算的财宝作废；
//   - 连续死战：官方历史最高关数 ≥ 50（BattleView MIN_CONTINUE_FIGHT_STAGE）才开放，本服对所有人开放（用户要求）；
//     逐关照常模拟，输了就结束；
//   - C_RESTART_LAST：消耗一次次数，从上一轮结束时的关数、星数和加成接着打；
//   - 魔法 / 治疗加成 = 法术伤害 / 回复效果提升（battle.Fighter.MagicUp / HealUp）。

const (
	slTimes        = 3
	slMaxStage     = 501
	slSettleEvery  = 5
	slBossEvery    = 10
	slStarPerStage = 3   // × (4 − idx)
	slCashPerStage = 100 // × 关数 × (4 − idx)
	slBoxItem      = 10043
	slBossSoulPct  = 15
	slConMinBest   = 0 // 连续死战开放所需的历史最高关数（官方 50，本服对所有人开放）
	slStateMain    = 1 // battle_mgr BATTLE_STATE_MAIN
	slFirstNPC     = 900001
)

var (
	slNeedStar = [3]int{3, 15, 30}
	slAddValue = [3]int{6, 30, 60}
)

// ShilianState 是试炼存档（Player.Shilian）。
type ShilianState struct {
	Day      string `json:"day"`
	Times    int    `json:"times"`     // 今日剩余次数
	MaxStar  int    `json:"max_star"`  // 今日最佳
	MaxStage int    `json:"max_stage"` //
	Best     int    `json:"best"`      // 历史最高关数（连续死战的开放条件）
	Last     int    `json:"last"`      // 上一轮打到的关数（S_SEND_STATE.last_stage）
	LastRun  *slRun `json:"last_run,omitempty"`
	Runs     int    `json:"runs"` // 战报号 shilian-<runs>-<关数>
	Run      *slRun `json:"run,omitempty"`
}

type slRun struct {
	Stage   int       `json:"stage"` // 已通过的关数
	AllStar int       `json:"all_star"`
	CurStar int       `json:"cur_star"`
	Add     [5]int    `json:"add"` // 下标 = 加成类型 1–4
	Box     int       `json:"box"` // 未结算的财宝
	Souls   []int32   `json:"souls,omitempty"`
	Cash    int64     `json:"cash"`
	Foes    [3]*slFoe `json:"foes"`            // 本关的对手（魔王关只有 [0]）
	Offer   [][2]int  `json:"offer,omitempty"` // 待选的加成 {类型, 数值}；nil = 不在选加成
}

// slFoe 是一个对手：敌方卡牌类型按站位排列。
type slFoe struct {
	Types []int32 `json:"types"`
}

func (p *Player) sl() *ShilianState {
	if p.Shilian == nil {
		p.Shilian = &ShilianState{}
	}
	d := p.Shilian
	if today := time.Now().Format("2006-01-02"); d.Day != today {
		d.Day, d.Times, d.MaxStar, d.MaxStage = today, slTimes, 0, 0
	}
	return d
}

func slBoss(stage int) bool { return stage%slBossEvery == 0 }

func slLevel(stage int) int { return min(2*stage-1, 200) }

// slCount 是第 stage 关第 idx 个对手的人数。
func slCount(stage, idx int) int { return min(battle.FightSlots, 5+stage/4-(idx-1)) }

func slStarCap(stage int) int { return min(5, 1+stage/6) }

func slPick(star int, rng *rand.Rand) int32 {
	for s := star; s >= 1; s-- {
		if l := gamedata.PetsByStar[s]; len(l) > 0 {
			return l[rng.IntN(len(l))]
		}
	}
	return 0
}

// newFoe 随机第 stage 关第 idx 个对手的阵容（站位按星级从高到低）。
func newFoe(stage, idx int, rng *rand.Rand) *slFoe {
	top := slStarCap(stage)
	n := slCount(stage, idx)
	stars := make([]int, n)
	for i := range stars {
		stars[i] = top
		if i >= 2 {
			stars[i] = top - min(rng.IntN(top), rng.IntN(top))
		}
	}
	if slBoss(stage) {
		stars[0] = 5
	}
	slices.SortStableFunc(stars, func(a, b int) int { return cmp.Compare(b, a) })
	f := &slFoe{}
	for _, s := range stars {
		if t := slPick(s, rng); t != 0 {
			f.Types = append(f.Types, t)
		}
	}
	return f
}

// nextFoes 生成第 Stage+1 关的对手。
func (r *slRun) nextFoes(rng *rand.Rand) {
	st := r.Stage + 1
	r.Foes = [3]*slFoe{newFoe(st, 1, rng)}
	if !slBoss(st) {
		r.Foes[1], r.Foes[2] = newFoe(st, 2, rng), newFoe(st, 3, rng)
	}
}

// slScale 是第 stage 关敌方属性的倍率（百分比）；测试里换成极小 / 极大值，让战斗必胜 / 必败。
var slScale = func(stage int) int64 { return int64(100 + 8*(stage-1)) }

// slEnemy 是第 stage 关的敌方卡牌 t（站位 slot 1 起）。
func slEnemy(stage, slot int, t int32) *battle.Fighter {
	pi := gamedata.Pets[t]
	if pi == nil {
		return nil
	}
	lv := slLevel(stage)
	mul := slScale(stage)
	att := int32(int64(pi.Att(lv)) * mul / 100)
	hp := int32(int64(pi.HP(lv)) * mul / 100)
	var tals []int32
	for _, l := range pi.Talents {
		if len(l) > 0 {
			tals = append(tals, l[len(l)-1])
		}
	}
	return &battle.Fighter{ID: int32(slFirstNPC + slot - 1), Pos: 100 + slot, Name: pi.Name, Att: att, HP: hp, MaxHP: hp,
		Speed: pi.Speed, Icon: t, Race: pi.Race, Grade: lv, Talents: tals, Magic: pi.Magic,
		MagicLv: min(10, 1+(stage-1)/20)}
}

func (f *slFoe) team(stage int) []*battle.Fighter {
	var out []*battle.Fighter
	for i, t := range f.Types {
		if x := slEnemy(stage, i+1, t); x != nil {
			out = append(out, x)
		}
	}
	return out
}

func (f *slFoe) face() int32 {
	if f == nil || len(f.Types) == 0 {
		return 0
	}
	return f.Types[0]
}

// slTeam 是出战阵容加上本轮的加成。
func (p *Player) slTeam(r *slRun) []*battle.Fighter {
	team, _ := p.dyTeam(nil)
	for _, f := range team {
		f.Att = int32(int64(f.Att) * int64(100+r.Add[1]) / 100)
		f.HP = int32(int64(f.HP) * int64(100+r.Add[2]) / 100)
		f.MaxHP = f.HP
		f.MagicUp, f.HealUp = r.Add[3], r.Add[4]
	}
	return team
}

// rollOffer 随机 3 个不同类型的加成（数值 6 / 30 / 60）。
func (r *slRun) rollOffer(rng *rand.Rand) {
	types := rng.Perm(4)
	r.Offer = make([][2]int, 3)
	for i := range r.Offer {
		r.Offer[i] = [2]int{types[i] + 1, slAddValue[i]}
	}
}

// ---- 排行 ----

// slBoard 是今日试炼排行（内存：玩家结束一轮或打开试炼时登记，服务重启后从头积累）。
type slBoard struct {
	mu   sync.Mutex
	day  string
	rows map[int32]*slRow
}

type slRow struct {
	UID        int32
	Name       string
	Grade      int
	Icon       int
	Star       int
	Stage      int
	FightPoint int
	Team       []xym.Msg // S_SEND_PLAYER_TEAM.list
}

var slRank = &slBoard{}

// slGroup 是等级对应的排行组（config.lua getPlayerGroup）。
func slGroup(grade int) int {
	switch {
	case grade <= 30:
		return 30
	case grade <= 40:
		return 40
	}
	return 50
}

func (b *slBoard) put(r slRow) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if today := time.Now().Format("2006-01-02"); b.day != today {
		b.day, b.rows = today, map[int32]*slRow{}
	}
	if r.Star == 0 && r.Stage == 0 {
		delete(b.rows, r.UID)
		return
	}
	b.rows[r.UID] = &r
}

// list 是组内排行（星数、关数从高到低）。
func (b *slBoard) list(group int) []slRow {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.day != time.Now().Format("2006-01-02") {
		return nil
	}
	var out []slRow
	for _, r := range b.rows {
		if slGroup(r.Grade) == group {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(x, y slRow) int {
		return cmp.Or(cmp.Compare(y.Star, x.Star), cmp.Compare(y.Stage, x.Stage), cmp.Compare(x.UID, y.UID))
	})
	return out
}
