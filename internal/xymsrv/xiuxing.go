package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 修行（XIUXING_PTO，玩法栏「修行」→ view/XiuXingWangFaMainView.lua、XiuXingWangFaActiveView.lua、QiYuMainView.lua）：
// 派最多 3 张护法修行 1 小时，每 15 分钟累积一次银两、出一个「奇遇」，完成奇遇得奖励。
//
// 依据：
//   - 规则（XiuXingHelpView）：每次修行持续 60 分钟，每 15 分钟自动累积一次银两奖励，可随时结束；每 15 分钟累积一次奇遇，
//     完成奇遇得对应奖励；每周一确定本周星阵容，修行护法满足要求时完成奇遇有几率触发「周奇遇」得护法魂魄。
//     ActiveView：每次修行消耗 5 体力、每天 3 次（eblDes2）；S_OPEN_XIUXING{qiyu_name 周奇遇名, end_time, total_time,
//     reward_icon 周奇遇奖励, money 累计银两, state 0 未开始 / 1 修行中 / 2 已结束, left_count, fighter[type],
//     qiyu_list[pos, type, state 2 待完成 / 其他 已完成], week_pet_list[type], baodi_list[type 5 结缘 / 7 同修, progress, pressed]}；
//     「修行」发 C_START_XIUXING{fighter[type]}，「结束」发 C_END_XIUXING。
//   - QiYuMainView：C_OPEN_QIYU{index} → S_OPEN_QIYU；奇遇类型 1 / 5 / 7 只有文字（TypeThree），2 / 3 带奖励图标（TypeOne，
//     2 = 周奇遇显示元宝数 spend_gold），4 / 6 商人 / 竞拍（TypeTwo：cost 价格、time_left）。按钮文字由服务端的 keys 决定，
//     按下发 C_QIYU_KEY_PRESS{index, key, param}；要战斗时回 S_FIGHT_CA{ca, param}，客户端打开临时选阵（TempView），
//     确认发 C_FIGHT_WITH_TMP{index, leader_num, tmp_team}。奖励图标 reward_icon 是「掉落编码:数量」（如 WP10126:5）。
//
// 估计（没有抓包）：每 15 分钟银两 1000 + 100×人物等级；奇遇在普通（领取）、拦路妖魔（挑战，敌人取与人物等级相当的
// 精英关卡阵容）、云游商人（元宝买打折道具）里随机，修行护法里有本周星阵容的卡时 25% 换成周奇遇（该卡魂魄 ×5）；
// 全服奇遇、竞拍没做；结束时发累计银两，没完成的奇遇作废。
//
// 结缘 / 同修（XiuXingHelpView：「派出修行的5星护法有几率触发缘奇遇与同修奇遇，完成结缘奇遇可使5缘护法增加一条
// 人物缘，完成同修奇遇可使护法10%加成的人物缘进阶」「每次修行增加20%结缘值，结缘值满时，可点击结缘按钮，下次
// 派出可结缘护法修行必出结缘奇遇」「每次修行增加15%同修值」）：
//   - 可结缘：护法有 extend_relate（gamedata.ExtendRelates）且还没结缘；结缘奇遇（类型 5）完成后 Pet.Combine 加上它；
//   - 可同修：已结缘、那条缘分有 together_npet 且还没同修；同修奇遇（类型 7）完成后 Pet.Together 加上它（relate.go）；
//   - 每次开始修行结缘值 +20、同修值 +15（满 100）；满了点按钮（C_OPEN_BAODI{type 5 / 7}）记下，下次派出有可结缘 /
//     可同修的护法时第一个奇遇必是它，用掉后值清零。
// 估计：没点按钮时每个奇遇 10% 概率出结缘 / 同修奇遇（派出的护法里有可结缘 / 可同修的才会出）。

const (
	xxTime     = 3600
	xxTick     = 900 // 每 15 分钟一次银两 / 奇遇
	xxTicks    = xxTime / xxTick
	xxDaily    = 3
	xxCost     = 5 // 体力
	xxMaxPets  = 3
	xxWeekPets = 3
	xxWeekRate = 25
)

// 奇遇类型（XiuXingWangFaActiveView QIYU_*）。
const (
	qyWeek     = 2
	qyNormal   = 3
	qyMerchant = 4
	qyJieyuan  = 5 // 结缘奇遇
	qyTongxiu  = 7 // 同修奇遇

	xxJieyuanStep = 20 // 每次修行加的结缘值 %
	xxTongxiuStep = 15 // 每次修行加的同修值 %
	xxYuanRate    = 10 // 没点按钮时每个奇遇出结缘 / 同修奇遇的概率 %

	jieyuanStone = 10416 // 结缘石：完成结缘奇遇消耗 1 个（道具说明「护法结缘时需要消耗的道具」）
	tongxiuStone = 10417 // 同修石：完成同修奇遇消耗 1 个
)

// xxNow 是当前时间（测试可改）。
var xxNow = time.Now

// XQiyu 是一次修行里的一个奇遇。
type XQiyu struct {
	Type int    `json:"type"`
	Kind string `json:"kind"` // gift / fight / merchant / week
	Code string `json:"code"` // 奖励掉落编码
	N    int32  `json:"n"`
	Cost int32  `json:"cost,omitempty"` // 商人：元宝价
	Done bool   `json:"done,omitempty"`
	Pet  int32  `json:"pet,omitempty"` // 结缘 / 同修奇遇：护法类型
	Rid  int32  `json:"rid,omitempty"` // 结缘 / 同修奇遇：缘分号
}

// XiuxingState 是修行存档（Player.Xiuxing）。
type XiuxingState struct {
	Day    string   `json:"day"`
	Used   int      `json:"used"`            // 今日已修行次数
	Start  int64    `json:"start,omitempty"` // 本次开始时间（0 = 未开始）
	Pets   []int32  `json:"pets,omitempty"`  // 派出的护法类型
	Qiyu   []*XQiyu `json:"qiyu,omitempty"`
	Seed   uint64   `json:"seed,omitempty"`
	PerPay int32    `json:"per_pay,omitempty"` // 每 15 分钟的银两
	// 结缘值 / 同修值（%，满 100 可点按钮）与按钮是否已按下（下次必出）；不随天数清零。
	Jieyuan   int  `json:"jieyuan,omitempty"`
	Tongxiu   int  `json:"tongxiu,omitempty"`
	JieyuanOn bool `json:"jieyuan_on,omitempty"`
	TongxiuOn bool `json:"tongxiu_on,omitempty"`
	// 本次修行：可出的结缘 / 同修奇遇（开始时按派出的护法算好）与必出的（按了按钮的）
	YuanCand []*XQiyu `json:"yuan_cand,omitempty"`
	Forced   []*XQiyu `json:"forced,omitempty"`
}

// yuanCands 是派出的护法里可结缘 / 可同修的（每张卡结缘、同修各至多一条）。
// 之前每张卡只取第一条可用的缘分：卡上还有没结缘的缘分时，已结缘那条永远出不了同修奇遇，同修值满 100 按了按钮
// 也一直不触发、不清零（用户报）。
func (p *Player) yuanCands(types []int32) []*XQiyu {
	var out []*XQiyu
	for _, t := range types {
		pet := p.petByType(t)
		if pet == nil {
			continue
		}
		var jy, tx bool
		for _, rid := range gamedata.ExtendRelates[t] {
			switch {
			case !slices.Contains(pet.Combine, rid):
				if !jy {
					jy = true
					out = append(out, &XQiyu{Type: qyJieyuan, Kind: "jieyuan", Pet: t, Rid: rid})
				}
			case !slices.Contains(pet.Together, rid) && gamedata.Relates[rid] != nil && len(gamedata.Relates[rid].TogetherNPet) > 0:
				if !tx {
					tx = true
					out = append(out, &XQiyu{Type: qyTongxiu, Kind: "tongxiu", Pet: t, Rid: rid})
				}
			}
		}
	}
	return out
}

func (p *Player) xiuxing() *XiuxingState {
	if p.Xiuxing == nil {
		p.Xiuxing = &XiuxingState{}
	}
	x := p.Xiuxing
	if d := xxNow().Format("2006-01-02"); x.Day != d {
		x.Day, x.Used = d, 0
	}
	return x
}

// ticks 是已经过的 15 分钟数（≤ 4）。
func (x *XiuxingState) ticks(now int64) int {
	if x.Start == 0 {
		return 0
	}
	return int(min(int64(xxTicks), (now-x.Start)/xxTick))
}

// xxWeek 是本周的星阵容（3 张 5 星卡，按 ISO 周轮换）与周奇遇名。
func xxWeek(now time.Time) ([]int32, string) {
	y, w := now.ISOWeek()
	l := gamedata.PetsByStar[5]
	if len(l) == 0 {
		return nil, ""
	}
	rng := rand.New(rand.NewPCG(uint64(y), uint64(w)))
	var out []int32
	for len(out) < min(xxWeekPets, len(l)) {
		if t := l[rng.IntN(len(l))]; !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	name := ""
	if pi := gamedata.Pets[out[0]]; pi != nil {
		name = pi.Name + "的试炼"
	}
	return out, name
}

// xxGifts / xxFightRewards / xxGoods：普通奇遇、拦路妖魔、云游商人的奖励池（掉落编码, 数量）。
var (
	xxGifts        = [][2]string{{"WP10126", "3"}, {"WP10127", "3"}, {"WP11489", "2"}, {"WP10002", "2"}, {"WP10003", "2"}, {"WP10130", "2"}}
	xxFightRewards = [][2]string{{"WP10126", "8"}, {"WP10127", "8"}, {"WP11490", "2"}, {"WP10140", "1"}, {"WP10095", "1"}}
	xxGoods        = []struct {
		code string
		n    int32
		gold int32
	}{{"WP10096", 1, 60}, {"WP10308", 1, 80}, {"WP10117", 1, 300}, {"WP10140", 2, 40}}
)

// rollQiyu 补齐到 n 个奇遇（每个按 Seed 和序号确定，重开界面不会变）。
func (x *XiuxingState) rollQiyu(n int, week []int32) {
	for len(x.Qiyu) < n {
		i := len(x.Qiyu)
		rng := rand.New(rand.NewPCG(x.Seed, uint64(i)))
		var q *XQiyu
		var hit []int32
		for _, t := range x.Pets {
			if slices.Contains(week, t) {
				hit = append(hit, t)
			}
		}
		var yuan []*XQiyu // 还没出过的结缘 / 同修奇遇
		for _, c := range x.YuanCand {
			if !slices.ContainsFunc(x.Qiyu, func(q *XQiyu) bool { return q.Kind == c.Kind && q.Pet == c.Pet && q.Rid == c.Rid }) {
				yuan = append(yuan, c)
			}
		}
		switch r := rng.IntN(100); {
		case i < len(x.Forced):
			q = new(XQiyu)
			*q = *x.Forced[i]
		case len(yuan) > 0 && rng.IntN(100) < xxYuanRate:
			q = new(XQiyu)
			*q = *yuan[rng.IntN(len(yuan))]
		case len(hit) > 0 && r < xxWeekRate:
			q = &XQiyu{Type: qyWeek, Kind: "week", Code: "HP" + strconv.Itoa(int(hit[rng.IntN(len(hit))])), N: 5}
		case r < 55:
			g := xxGifts[rng.IntN(len(xxGifts))]
			n, _ := strconv.Atoi(g[1])
			q = &XQiyu{Type: qyNormal, Kind: "gift", Code: g[0], N: int32(n)}
		case r < 85:
			g := xxFightRewards[rng.IntN(len(xxFightRewards))]
			n, _ := strconv.Atoi(g[1])
			q = &XQiyu{Type: qyNormal, Kind: "fight", Code: g[0], N: int32(n)}
		default:
			g := xxGoods[rng.IntN(len(xxGoods))]
			q = &XQiyu{Type: qyMerchant, Kind: "merchant", Code: g.code, N: g.n, Cost: g.gold}
		}
		x.Qiyu = append(x.Qiyu, q)
	}
}

// xxGate 是与人物等级相当的精英关卡（拦路妖魔用它的阵型）。
func xxGate(grade int) gamedata.GateMode {
	want := max(1, min(12, grade/5))*10000 + 101
	gids := make([]int, 0, len(gamedata.Gates))
	for gid := range gamedata.Gates {
		gids = append(gids, gid)
	}
	sort.Ints(gids)
	for _, gid := range gids {
		if gid >= want && gamedata.Gates[gid].Mode[1].CA != 0 {
			return gamedata.Gates[gid].Mode[1]
		}
	}
	if len(gids) > 0 {
		return gamedata.Gates[gids[len(gids)-1]].Mode[1]
	}
	return gamedata.GateMode{}
}

func init() {
	Handle("XIUXING_PTO.C_OPEN_XIUXING", func(s *Session, p *Player, m xym.Msg) { s.xxOpen(p) })
	Handle("XIUXING_PTO.C_START_XIUXING", onXXStart)
	Handle("XIUXING_PTO.C_END_XIUXING", onXXEnd)
	Handle("XIUXING_PTO.C_OPEN_QIYU", func(s *Session, p *Player, m xym.Msg) { s.xxQiyu(p, int(m["index"].(int64))) })
	Handle("XIUXING_PTO.C_QIYU_KEY_PRESS", onXXKey)
	Handle("XIUXING_PTO.C_FIGHT_WITH_TMP", onXXFight)
	Handle("XIUXING_PTO.C_OPEN_BAODI", onXXBaodi)
	Handle("XIUXING_PTO.C_LOOK_PRICE", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("XIUXING_PTO.S_LOOK_PRICE", xym.Msg{"price_list": []xym.Msg{}})
	})
}

func (s *Session) xxOpen(p *Player) {
	now := xxNow()
	x := p.xiuxing()
	week, wname := xxWeek(now)
	state, end := 0, int64(0)
	if x.Start != 0 {
		x.rollQiyu(x.ticks(now.Unix()), week)
		end = x.Start + xxTime
		state = 1
		if now.Unix() >= end {
			state = 2
		}
	}
	fs := []xym.Msg{}
	for _, t := range x.Pets {
		fs = append(fs, xym.Msg{"type": t})
	}
	qs := []xym.Msg{}
	for i, q := range x.Qiyu {
		st := 2
		if q.Done {
			st = 1
		}
		qs = append(qs, xym.Msg{"pos": i + 1, "type": q.Type, "state": st})
	}
	ws := []xym.Msg{}
	for _, t := range week {
		ws = append(ws, xym.Msg{"type": t})
	}
	icon := ""
	if len(week) > 0 {
		icon = "HP" + strconv.Itoa(int(week[0])) + ":5"
	}
	s.SendExt("XIUXING_PTO.S_OPEN_XIUXING", xym.Msg{"qiyu_name": wname, "end_time": end, "total_time": xxTime,
		"reward_icon": icon, "money": x.PerPay * int32(x.ticks(now.Unix())), "state": state,
		"left_count": max(0, xxDaily-x.Used), "fighter": fs, "qiyu_list": qs, "week_pet_list": ws,
		"baodi_list": []xym.Msg{{"type": qyJieyuan, "progress": x.Jieyuan, "pressed": b2i(x.JieyuanOn)},
			{"type": qyTongxiu, "progress": x.Tongxiu, "pressed": b2i(x.TongxiuOn)}}})
}

func onXXStart(s *Session, p *Player, m xym.Msg) {
	x := p.xiuxing()
	fs, _ := m["fighter"].([]xym.Msg)
	var pets []int32
	for _, f := range fs {
		t := int32(f["type"].(int64))
		if !slices.Contains(pets, t) && slices.ContainsFunc(p.Pets, func(pet *Pet) bool { return pet.Type == t }) {
			pets = append(pets, t)
		}
	}
	switch {
	case x.Start != 0:
		s.retMsg("正在修行中。")
	case x.Used >= xxDaily:
		s.retMsg("今天的修行次数已用完，明天再来吧。")
	case len(pets) == 0 || len(pets) > xxMaxPets:
		s.retMsg("请先选择修行的护法。")
	case !s.roleUpdate(func(ro *Role) bool {
		if ro.TPoint < xxCost {
			return false
		}
		ro.TPoint -= xxCost
		return true
	}, "tpoint"):
		s.retMsg(fmt.Sprintf("体力不足，每次修行消耗%d体力。", xxCost))
	default:
		now := xxNow().Unix()
		x.Used++
		x.Start, x.Pets, x.Qiyu, x.Seed = now, pets, nil, uint64(now)^uint64(p.UID)<<20
		x.PerPay = int32(1000 + 100*s.role.Grade)
		// 结缘 / 同修：按了按钮且这次派出了可结缘 / 可同修的护法 → 前几个奇遇必出，用掉后值清零；之后再加本次的值
		x.YuanCand, x.Forced = p.yuanCands(pets), nil
		for _, k := range []struct {
			kind string
			on   *bool
			val  *int
		}{{"jieyuan", &x.JieyuanOn, &x.Jieyuan}, {"tongxiu", &x.TongxiuOn, &x.Tongxiu}} {
			if !*k.on {
				continue
			}
			if i := slices.IndexFunc(x.YuanCand, func(q *XQiyu) bool { return q.Kind == k.kind }); i >= 0 {
				x.Forced = append(x.Forced, x.YuanCand[i])
				*k.on, *k.val = false, 0
			}
		}
		x.Jieyuan = min(100, x.Jieyuan+xxJieyuanStep)
		x.Tongxiu = min(100, x.Tongxiu+xxTongxiuStep)
		p.saveLog(s)
	}
	s.xxOpen(p)
}

// onXXEnd：结束修行，发累计银两；没完成的奇遇作废。
func onXXEnd(s *Session, p *Player, m xym.Msg) {
	x := p.xiuxing()
	if x.Start != 0 {
		cash := x.PerPay * int32(x.ticks(xxNow().Unix()))
		x.Start, x.Qiyu = 0, nil
		p.saveLog(s)
		if cash > 0 {
			s.roleUpdate(func(ro *Role) bool { ro.Cash += cash; return true }, "cash")
			r := rewardMsg(1, []xym.Msg{{"rtype": rtItem, "type": itemCash, "count": cash}})
			s.SendExt("ETC_PTO.S_REWARD_MSG", r)
		}
	}
	s.xxOpen(p)
}

// xxQiyuAt 是第 index 个（1 起）已出现的奇遇。
func (p *Player) xxQiyuAt(index int) *XQiyu {
	x := p.xiuxing()
	week, _ := xxWeek(xxNow())
	if x.Start != 0 {
		x.rollQiyu(x.ticks(xxNow().Unix()), week)
	}
	if index < 1 || index > len(x.Qiyu) {
		return nil
	}
	return x.Qiyu[index-1]
}

func (s *Session) xxQiyu(p *Player, index int) {
	q := p.xxQiyuAt(index)
	msg := xym.Msg{"type": qyNormal, "name": "", "desc": "", "win_desc": "", "reward_icon1": "", "reward_icon2": "",
		"is_finished": 1, "keys": []xym.Msg{}, "time_left": 0, "cost": 0, "last_cost": 0, "min_add": 0, "total_user": 0, "spend_gold": 0}
	if q != nil {
		icon := q.Code + ":" + strconv.Itoa(int(q.N))
		msg["type"], msg["reward_icon1"], msg["is_finished"] = q.Type, icon, b2i(q.Done)
		keys := func(l ...string) {
			ks := []xym.Msg{}
			for _, k := range l {
				ks = append(ks, xym.Msg{"key": k})
			}
			msg["keys"] = ks
		}
		switch q.Kind {
		case "gift":
			msg["name"], msg["desc"], msg["win_desc"] = "仙人指路", "修行途中偶遇一位云游仙人，指点了护法几招。", "领取仙人的馈赠："
			keys("领取")
		case "fight":
			msg["name"], msg["desc"], msg["win_desc"] = "妖魔拦路", "修行途中一伙妖魔拦住了去路！", "击败妖魔可获得："
			keys("挑战", "放弃")
		case "merchant":
			msg["name"], msg["desc"] = "云游商人", fmt.Sprintf("一位云游商人路过，愿以%d元宝出售他的宝贝。", q.Cost)
			msg["cost"], msg["time_left"] = q.Cost, max(0, p.xiuxing().Start+xxTime-xxNow().Unix())
			keys("购买", "放弃")
		case "week":
			msg["name"], msg["desc"], msg["win_desc"] = "周奇遇", "本周星阵容的护法在修行中得到了同伴的魂魄。", "领取："
			keys("领取")
		case "jieyuan", "tongxiu":
			pn, rn := "", ""
			if pi := gamedata.Pets[q.Pet]; pi != nil {
				pn = pi.Name
			}
			if r := gamedata.Relates[q.Rid]; r != nil {
				rn = r.Name
			}
			msg["reward_icon1"] = ""
			if q.Kind == "jieyuan" {
				msg["name"], msg["desc"] = "结缘奇遇", fmt.Sprintf("%s在修行中结下一段缘分，消耗结缘石×1完成后获得人物缘【%s】。", pn, rn)
				keys("结缘")
			} else {
				msg["name"], msg["desc"] = "同修奇遇", fmt.Sprintf("%s与同伴一同修行，消耗同修石×1完成后人物缘【%s】进阶。", pn, rn)
				keys("同修")
			}
		}
	}
	s.SendExt("XIUXING_PTO.S_OPEN_QIYU", msg)
}

// xxGive 发奇遇奖励并标记完成，回 S_REWARD_MSG（type）。
func (s *Session) xxGive(p *Player, q *XQiyu, typ int, rid string) {
	q.Done = true
	d := &bagDelta{}
	p.giveCode(q.Code, q.N, s.actRng(), d)
	p.saveLog(s)
	r := rewardMsg(typ, s.sendDelta(p, d))
	if rid != "" {
		r["record"], r["no_fight_again"] = rid, 1
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
}

func onXXKey(s *Session, p *Player, m xym.Msg) {
	index := int(m["index"].(int64))
	key, _ := m["key"].(string)
	q := p.xxQiyuAt(index)
	switch {
	case q == nil || q.Done:
		s.retMsg("这个奇遇已经结束了。")
	case key == "放弃":
		q.Done = true
		p.saveLog(s)
	case key == "领取" && (q.Kind == "gift" || q.Kind == "week"):
		s.xxGive(p, q, 1, "")
	case key == "结缘" && q.Kind == "jieyuan", key == "同修" && q.Kind == "tongxiu":
		pet := p.petByType(q.Pet)
		if pet == nil {
			s.retMsg("这名护法已经不在了。")
			q.Done = true
			break
		}
		stone, name := jieyuanStone, "结缘石"
		if q.Kind == "tongxiu" {
			stone, name = tongxiuStone, "同修石"
		}
		ch, rm := p.takeItems(stone, 1)
		if ch == nil && rm == nil {
			s.retMsg(fmt.Sprintf("%s不足，可在商城购买。", name))
			break
		}
		s.sendDelta(p, &bagDelta{items: ch, removed: rm})
		q.Done = true
		if q.Kind == "jieyuan" && !slices.Contains(pet.Combine, q.Rid) {
			pet.Combine = append(pet.Combine, q.Rid)
		}
		if q.Kind == "tongxiu" && slices.Contains(pet.Combine, q.Rid) && !slices.Contains(pet.Together, q.Rid) {
			pet.Together = append(pet.Together, q.Rid)
		}
		p.saveLog(s)
		s.petUpdate(p, pet)
		if r := gamedata.Relates[q.Rid]; r != nil {
			if q.Kind == "jieyuan" {
				s.retMsg(fmt.Sprintf("结缘成功，获得人物缘【%s】。", r.Name))
			} else {
				s.retMsg(fmt.Sprintf("同修成功，人物缘【%s】已进阶。", r.Name))
			}
		}
	case key == "购买" && q.Kind == "merchant":
		if !s.roleUpdate(func(ro *Role) bool {
			if ro.Gold < q.Cost {
				return false
			}
			ro.Gold -= q.Cost
			return true
		}, "gold") {
			s.retMsg("元宝不足。")
			break
		}
		s.xxGive(p, q, 1, "")
	case key == "挑战" && q.Kind == "fight":
		mode := xxGate(s.role.Grade)
		s.SendExt("XIUXING_PTO.S_FIGHT_CA", xym.Msg{"ca": mode.CA, "param": mode.Param})
		return
	default:
		s.retMsg("无效的操作。")
	}
	s.xxQiyu(p, index)
	s.xxOpen(p)
}

// onXXBaodi：结缘值 / 同修值满 100 时点按钮，下次派出可结缘 / 可同修的护法修行必出对应奇遇。
func onXXBaodi(s *Session, p *Player, m xym.Msg) {
	x := p.xiuxing()
	typ := int(m["type"].(int64))
	val, on, name := &x.Jieyuan, &x.JieyuanOn, "结缘"
	if typ == qyTongxiu {
		val, on, name = &x.Tongxiu, &x.TongxiuOn, "同修"
	}
	switch {
	case *on:
		s.retMsg(fmt.Sprintf("已经开启，下次派出可%s的护法修行必出%s奇遇。", name, name))
	case *val < 100:
		s.retMsg(fmt.Sprintf("%s值还不足。", name))
	default:
		*on = true
		p.saveLog(s)
		s.retMsg(fmt.Sprintf("下次派出可%s的护法修行必出%s奇遇！", name, name))
	}
	s.xxOpen(p)
}

// onXXFight：拦路妖魔的战斗（临时阵容），赢了发奖励并完成奇遇。
func onXXFight(s *Session, p *Player, m xym.Msg) {
	index := int(m["index"].(int64))
	q := p.xxQiyuAt(index)
	if q == nil || q.Done || q.Kind != "fight" {
		s.sendMsg("这个奇遇已经结束了。")
		return
	}
	team1, leader, _ := p.tmpTeam(m)
	if len(team1) == 0 {
		s.sendMsg("你的阵容为空，请添加护法！")
		return
	}
	mode := xxGate(s.role.Grade)
	team2 := battle.EnemyTeam(mode)
	if len(team2) == 0 {
		s.sendMsg("妖魔已经离开了。")
		return
	}
	r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
	res := s.simulate(p, team1, team2, [2]int32{leader, 0}, battle.Formation{}, s.actRng())
	rid := "xiuxing:" + strconv.FormatInt(recordSeq.Add(1), 10)
	s.fightRecord(rid, "妖魔", team2[0].Grade, r1, r2, res.Record)
	if !res.Win {
		r := rewardMsg(3, nil)
		r["record"], r["no_fight_again"] = rid, 1
		s.SendExt("ETC_PTO.S_REWARD_MSG", r)
		return
	}
	s.xxGive(p, q, 2, rid)
}
