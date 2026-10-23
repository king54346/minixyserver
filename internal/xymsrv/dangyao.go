package xymsrv

import (
	"math/rand/v2"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 荡妖（妖窟）与功德商店的规则。协议见 h_dangyao.go。
//
// 依据（客户端）：view/DangyaoView.lua（格子类型 MAZE_TYPE_*、每层 16 格、S_OPEN_FAIL / S_DANGYAO_END 流程、
// 复活价 (阵亡数 − 免费次数) × 2 元宝、遁地符 10082 或 50 元宝「携宝潜逃」、妖气满 200 必遇魔王），
// logic/dangyao_mgr.lua（妖窟层数、功德称号等级与上限、商店商品表，见 gamedata.Dangyao），
// ui 文案「重置妖窑后可能会刷新功德殿内物品」「今日限购」。地图号 = 妖窟号 × 100 + 层，妖窟 1–12 普通、101–112 特殊。
//
// 依据（官方抓包 session-01，特殊妖窟 108 一整轮）：
//   - 重置特殊妖窟扣 50 元宝（425 → 375）；重置时功德截到称号上限（327 → 300，3 级上限 300），妖气 +25（50 → 75）；
//   - 一键荡妖扣体力 = 未翻开的格子数（每层 15 = 16 − 入口），翻开全层并清掉小怪和宝箱；
//   - 「功」：小怪 16、宝箱 32、头目 48（5 层的一键荡妖收益 112 / 144 / 128 / 128 / 128 都能拆成这个组合）；
//   - 打完最后一层的头目：S_DANGYAO_END{state 2}，功德 += 本轮的功（300 + 880 = 1180），S_REWARD_MSG{type 4, ling}；
//   - 怪物阵型取 fight_ca 20708–20780（怪物 25000–25580），属性 = 基础 + 成长 × param / 1000，特殊妖窟 8 的 param ≈ 2325–2660；
//   - 商店：max_gongde = 累计消耗的功德（每次购买正好加上花费），称号等级由它决定；S_OPEN_SHOP 只列上架的商品。
//
// 估计（没有直接证据，待抓包）：
//   - 普通妖窟重置免费；翻单格扣 1 体力（由一键荡妖 15 = 15 格反推）；
//   - 每层格子：入口 1、下层入口 1（最后一层没有）、头目 1、小怪 3–5、宝箱 2，其余空地；
//   - 魔王（妖气满 200 时出现在最后一层，代替头目）功 96，打完妖气清零；
//   - 战斗 param：特殊妖窟 290 × 窟号、普通妖窟 190 × 窟号，每深一层 +3%；小怪 ×0.8，魔王 ×1.3；
//   - 商店上架：每件按 (jichu [+ 特殊妖窟时 zhongyao]) ‰ 概率上架，限购次数每次刷新重置；每日免费复活 3 次；
//   - 本周固定 x 星魂（奖励号 0）：本周 x 星卡池里固定一张的魂魄 1 个。

const (
	dyBlank   = 0
	dyMonster = 1
	dyBox     = 2
	dyLeader  = 3
	dyBoss    = 4
	dyUp      = 5
	dyDown    = 6
	dyMark    = 7 // 未翻开（客户端用，服务端不发）

	dyGrids        = 16
	dyRatioStep    = 25
	dyRatioMax     = 200
	dyFreeRelive   = 3
	dyReliveGold   = 2
	dyEscapeGold   = 50
	dyEscapeItem   = 10082 // 遁地符
	dySpecialCost  = 50    // 重置特殊妖窟的元宝
	dyStateFail    = 1
	dyStateSuccess = 2
)

// dyLing 是各类格子的功。
var dyLing = map[int]int{dyMonster: 16, dyBox: 32, dyLeader: 48, dyBoss: 96}

// DangyaoState 是荡妖存档（Player.Dangyao）。
type DangyaoState struct {
	Gongde int    `json:"gongde"`
	Spent  int64  `json:"spent"` // 累计消耗的功德（S_OPEN_SHOP.max_gongde，决定称号等级）
	Ratio  int    `json:"ratio"` // 妖气（S_SEND_BAODI_RATIO.base_ratio）
	Day    string `json:"day"`   // 每日免费复活次数的日期
	Free   int    `json:"free"`
	Dead   []int  `json:"dead,omitempty"` // 阵亡的出战位
	Fights int    `json:"fights"`         // 战报编号
	Run    *dyRun `json:"run,omitempty"`  // 进行中的一轮（nil = 没有，需重置妖窟）
	// Shop 是功德商店当前上架：商店类型 -> 商品（重置妖窟时重新上架）。
	Shop    map[int][]*dyShopSlot `json:"shop,omitempty"`
	Special bool                  `json:"special,omitempty"` // 上一次重置的是特殊妖窟（上架权重用）
}

type dyRun struct {
	Stage  int              `json:"stage"`
	Floor  int              `json:"floor"`
	Ling   int              `json:"ling"`
	Floors map[int]*dyFloor `json:"floors"`
}

type dyFloor struct {
	Grids [dyGrids]dyGrid `json:"grids"`
}

type dyGrid struct {
	Type int  `json:"t"`
	Open bool `json:"o,omitempty"`
}

type dyShopSlot struct {
	Pos  int `json:"pos"`
	Left int `json:"left"`
}

func (p *Player) dy() *DangyaoState {
	if p.Dangyao == nil {
		p.Dangyao = &DangyaoState{}
	}
	d := p.Dangyao
	if today := time.Now().Format("2006-01-02"); d.Day != today {
		d.Day, d.Free = today, dyFreeRelive
	}
	return d
}

// dyFloors 是妖窟 stage 的层数（0 = 没有这个妖窟）。
func dyFloors(stage int) int { return gamedata.Dangyao.Floors[strconv.Itoa(stage)] }

func dyMapID(stage, floor int) int { return stage*100 + floor }

// dyStageOpen：普通妖窟 s 在普通闯关打到第 s 张图后开放，特殊妖窟 100+s 看精英闯关。
func (p *Player) dyStageOpen(stage int) bool {
	if dyFloors(stage) == 0 {
		return false
	}
	typ, s := 0, stage
	if stage > 100 {
		typ, s = 1, stage-100
	}
	return p.Prog[typ] != nil && p.Prog[typ].Last/100 >= s
}

// gongdeLevel 是功德称号等级（1 起）：累计消耗达到上一级的 Need 即升级。
func (d *DangyaoState) gongdeLevel() int {
	lv := 1
	for i, l := range gamedata.Dangyao.Levels {
		if i+1 < len(gamedata.Dangyao.Levels) && d.Spent >= l.Need {
			lv = i + 2
		}
	}
	return lv
}

func (d *DangyaoState) gongdeMax() int {
	ls := gamedata.Dangyao.Levels
	if lv := d.gongdeLevel(); lv-1 < len(ls) {
		return ls[lv-1].Max
	}
	return 0
}

// newFloor 生成一层：入口（第 1 层空地，其后是上层入口）已翻开，其余待翻开。
func newFloor(floor, floors int, boss bool, rng *rand.Rand) *dyFloor {
	f := &dyFloor{}
	idx := rng.Perm(dyGrids)
	k := 0
	put := func(t int) {
		f.Grids[idx[k]].Type = t
		k++
	}
	entrance := idx[0]
	if floor > 1 {
		put(dyUp)
	} else {
		put(dyBlank)
	}
	f.Grids[entrance].Open = true
	if floor < floors {
		put(dyDown)
	}
	if boss && floor == floors {
		put(dyBoss)
	} else {
		put(dyLeader)
	}
	for range 3 + rng.IntN(3) {
		put(dyMonster)
	}
	for range 2 {
		put(dyBox)
	}
	return f
}

func (d *DangyaoState) floor() *dyFloor { return d.Run.Floors[d.Run.Floor] }

// openList 是一层已翻开的格子（S_OPEN_DANGYAO.list）。
func openList(f *dyFloor) []xym.Msg {
	l := []xym.Msg{}
	for i, g := range f.Grids {
		if g.Open {
			l = append(l, gridMsg(i, g))
		}
	}
	return l
}

func gridMsg(i int, g dyGrid) xym.Msg {
	times := 0
	if g.Type == dyLeader || g.Type == dyBoss {
		times = 1 // 头目 / 魔王显示 1/1（抓包 times 1 max 1）
	}
	return xym.Msg{"grid": i, "type": g.Type, "times": times, "max": times}
}

// dyParam 是妖窟怪物的等级参数（估计，见文件头）。
func dyParam(stage, floor, typ int) int {
	base := 190 * stage
	if stage > 100 {
		base = 290 * (stage - 100)
	}
	p := base * (100 + 3*(floor-1)) / 100
	switch typ {
	case dyMonster:
		p = p * 8 / 10
	case dyBoss:
		p = p * 13 / 10
	}
	return p
}

// dyParamFor 是取怪物等级参数的函数（测试里换成固定值，让战斗必胜 / 必败）。
var dyParamFor = dyParam

// dyEnemy 随机一个荡妖阵型。
func dyEnemy(stage, floor, typ int, rng *rand.Rand) []*battle.Fighter {
	forms := gamedata.Dangyao.Formations
	if len(forms) == 0 {
		return nil
	}
	ca := forms[rng.IntN(len(forms))]
	return battle.EnemyTeam(gamedata.GateMode{CA: ca, Param: dyParamFor(stage, floor, typ)})
}

// dyTeam 是出战阵容里没阵亡的卡（站位 = 出战位）。
func (p *Player) dyTeam(dead []int) ([]*battle.Fighter, []*Pet) {
	isDead := map[int]bool{}
	for _, fp := range dead {
		isDead[fp] = true
	}
	m := xym.Msg{}
	for fp := 1; fp <= battle.FightSlots; fp++ {
		if !isDead[fp] {
			m["pos"+strconv.Itoa(fp)] = int64(fp)
		}
	}
	return p.playerTeam(m)
}

// dyRollShop 是重新上架商店的函数（测试可换成全部上架）。
var dyRollShop = (*DangyaoState).rollShop

// rollShop 重新上架功德商店（每件按权重 ‰ 概率上架，限购重置）。
func (d *DangyaoState) rollShop(special bool, rng *rand.Rand) {
	d.Special = special
	d.Shop = map[int][]*dyShopSlot{}
	lv := d.gongdeLevel()
	for i := range gamedata.Dangyao.Shop {
		it := &gamedata.Dangyao.Shop[i]
		if it.Need > lv {
			continue
		}
		w := it.Weight[0]
		if special {
			w += it.Weight[2]
		}
		if rng.IntN(1000) < w {
			d.Shop[it.Type] = append(d.Shop[it.Type], &dyShopSlot{Pos: it.Pos, Left: it.Left})
		}
	}
}

func dyShopItem(pos int) *gamedata.DangyaoShopItem {
	for i := range gamedata.Dangyao.Shop {
		if gamedata.Dangyao.Shop[i].Pos == pos {
			return &gamedata.Dangyao.Shop[i]
		}
	}
	return nil
}

// weeklySoul 是「本周固定 star 星魂」本周对应的卡（按年内第几周轮换）。
func weeklySoul(star int, now time.Time) int32 {
	l := gamedata.PetsByStar[star]
	if len(l) == 0 {
		return 0
	}
	_, week := now.ISOWeek()
	return l[week%len(l)]
}
