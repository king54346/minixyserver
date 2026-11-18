package xymsrv

import (
	"math/rand/v2"
	"slices"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 洞府建设与修为（福利页「修为」、内务「建设」→ view/PartyXiuWeiView.lua、view/PartyBuildView.lua）。
//
// 两层修为：
//   - 洞府建筑（六座：先祖祠 human、缥缈峰 god、蛮荒谷 beast、噬魂窟 ghost、战神山 attack、长生殿 hp）：
//     成员完成「建设任务」时给当前建设目标（current_xiuwei，府主 / 洞主 C_MODIFY_DONGFU_CURRENT_XIUWEI 设）加经验。
//     S_SEND_DONGFU_WELFARE_INFO.xiuwei_list；六座等级之和 = xiuwei_lv_sum（抓包：5+5+5 = 15）。
//   - 个人修为：C_ADD_MEMBER_XIUWEI{type, count, need_type 1 元宝 / 2 银两}，每次 +5 经验
//     （ADD_MEMBER_XIUWEI_GIVE_EXP_UNIT），lv→lv+1 要 (lv+1)×10、20 级起 (lv+1)×20（calcDongfuMemberXiuweiLv；
//     抓包 lv5 exp_next 60、curr_exp 150 是累计经验）；每次花 元宝 10 / 银两 30000 + 灵气 1（20 级起 15 / 50000 + 2），
//     另扣魂值 10、20 级起再扣神魂 1（calcAddMemberXiuweiNeed）；每类每天 60 次（avail_count）。
//     个人等级上限按人物等级 5/15/20/25（getLimitGrade）；生效等级 effect_lv = min(个人, 洞府该建筑)，
//     「当修为超出洞府上限时，超出部分无效」。
//   - 效果（party_mgr GateTypeTabs 描述，每级 1%）：攻击 / 生命 +1% 面板；人族对仙、仙对魔、兽对兽、魔对人伤害 +1%；
//     先祖祠 土、缥缈峰 雷、蛮荒谷 水、噬魂窟 火 属性伤害 −1%；长生殿另有 1% 几率免疫道属性伤害（battle.Fighter 的
//     ElemRes / RaceDmg / DaoImmune，在 playerTeam 里注入）。
//
// 洞府升级 C_UPGRADE_DONGFU：要修为总和 ≥ 20×等级（4 级升 5 级要 100）、洞府元宝 500×等级、元气 200×等级（party_mgr
// getUpgradeNeedXiuWei / getUpgradeGold / getUpgradeLingqi），满级 5。额外建设任务 C_ADD_EXTRA_XIUWEI_TASK_TIMES：
// 花元气、洞府元宝各 100×等级（PartyBuildPubTaskView），当天全体成员可做的建设任务 +1。
// 扩建（屋舍 +5 人、灵池 +600 灵气上限，PartyBuildExtendView）。
//
// 估计：建筑等级上限 = 5×洞府等级（抓包 1 级洞府建筑 5 级；截图 5 级洞府 20–25 级）；建筑经验曲线同个人修为；
// 建设任务每天 1 个（+ 额外发布），上交一件指定的 1–3 星 1 级装备 / 法术书，建设目标 +10 经验、贡献 +5、元气 +20；
// 每次修炼给洞府元气 +1；扩建花洞府元宝 300×当前级、不超过洞府等级；额外建设任务每天最多 3 次；
// 洞府灵气池每小时进灵脉（3×等级 附近，抓包 1 级 3 点）点，成员每天第一次打开福利时按称号领 10–60 点（PartyLingQiGetTab）
// 进个人灵气；离开洞府个人灵气减半（退出提示「离开将扣除一半灵气」）。

const (
	dfXiuDaily      = 60
	dfXiuExpUnit    = 5
	dfXiuThreshold  = 20
	dfTaskBuildExp  = 10
	dfTaskContri    = 5
	dfTaskYuanqi    = 20
	dfMaxExtraTasks = 3
	dfMaxLv         = 5
)

var (
	dfTitleExp    = []int32{500, 1500, 3000, 5000, 8000}                                                // 称号升级要的贡献（PartyTitleExpTab，逐级累加）
	dfTitleLingqi = []int32{10, 20, 30, 40, 50, 60}                                                     // 每天领的灵气（PartyLingQiGetTab）
	dfRaceDmg     = map[string][2]int{"human": {1, 3}, "god": {3, 2}, "beast": {4, 4}, "ghost": {2, 1}} // 修为 -> {本方种族, 目标种族}
	dfElem        = map[string]int{"human": 1, "god": 4, "beast": 2, "ghost": 3}                        // 修为 -> 减伤的属性
)

// DFPlayer 是玩家自己的洞府数据（Player.DF）：个人修为、灵气、建设任务。
type DFPlayer struct {
	Exp       map[string]int32 `json:"exp,omitempty"`  // 各修为累计经验
	Day       string           `json:"day,omitempty"`  // 修炼次数所属日期
	Used      map[string]int   `json:"used,omitempty"` // 今天各修为已修炼次数
	Lingqi    int32            `json:"lingqi,omitempty"`
	GodSoul   int32            `json:"god_soul,omitempty"`
	LingqiDay string           `json:"lingqi_day,omitempty"`
	Caps      map[string]int   `json:"caps,omitempty"` // 所在洞府各建筑等级（生效上限；不在洞府为空）
	TaskDay   string           `json:"task_day,omitempty"`
	TaskDone  int              `json:"task_done,omitempty"`
	TaskKind  string           `json:"task_kind,omitempty"`
	TaskType  int32            `json:"task_type,omitempty"`
}

func (p *Player) df() *DFPlayer {
	if p.DF == nil {
		p.DF = &DFPlayer{}
	}
	d := p.DF
	if d.Exp == nil {
		d.Exp = map[string]int32{}
	}
	if t := today(); d.Day != t {
		d.Day, d.Used = t, map[string]int{}
	}
	if d.Used == nil {
		d.Used = map[string]int{}
	}
	return d
}

// DFBuild 是一座洞府建筑。
type DFBuild struct {
	Lv  int   `json:"lv"`
	Exp int32 `json:"exp"`
}

// xiuweiNeed 是 lv 升下一级要的经验。
func xiuweiNeed(lv int) int32 {
	if lv >= dfXiuThreshold {
		return int32(lv+1) * 20
	}
	return int32(lv+1) * 10
}

// xiuweiLv 由累计经验算等级与本级经验。
func xiuweiLv(total int32) (lv int, exp int32) {
	exp = total
	for exp >= xiuweiNeed(lv) {
		exp -= xiuweiNeed(lv)
		lv++
	}
	return lv, exp
}

// memberLimit 是个人修为的最高等级上限（洞府修为最高满级 25 级）。
// 之前按人物等级 (<=30 为 15 等) 限制导致卡级玩家（如停 30 级）在高级洞府（如洞府等级 20 级）中
// 修为达到 15 级时被错误置为 exp_next=0 显示“满级”且无法继续修炼。
// 实际上个人可修炼至满级 25 级，战斗中超出洞府上限的部分自动按洞府建筑等级截断无效（见 effLv）。
func memberLimit(grade int) int {
	return 25
}

func (df *Dongfu) build(t string) *DFBuild {
	if df.Builds == nil {
		df.Builds = map[string]*DFBuild{}
	}
	b := df.Builds[t]
	if b == nil {
		b = &DFBuild{}
		df.Builds[t] = b
	}
	return b
}

func (df *Dongfu) buildCap() int { return 5 * int(df.Lv) }

// addBuildExp 给建筑 t 加经验（到上限后经验不再加）。
func (df *Dongfu) addBuildExp(t string, n int32) {
	b := df.build(t)
	b.Exp += n
	for b.Lv < df.buildCap() && b.Exp >= xiuweiNeed(b.Lv) {
		b.Exp -= xiuweiNeed(b.Lv)
		b.Lv++
	}
	if b.Lv >= df.buildCap() {
		b.Exp = min(b.Exp, xiuweiNeed(b.Lv)-1)
	}
}

func (df *Dongfu) xiuweiSum() int {
	n := 0
	for _, t := range dfXiuweiTypes {
		n += df.build(t).Lv
	}
	return n
}

func (df *Dongfu) curXiuwei() string {
	if df.CurXiuwei == "" {
		return "attack"
	}
	return df.CurXiuwei
}

// accrueLingqi：灵气池按灵脉每小时累积，不超过上限。
func (df *Dongfu) accrueLingqi() {
	now := nowUnix()
	if df.LingqiAt == 0 || df.LingqiAt > now {
		df.LingqiAt = now
	}
	if h := (now - df.LingqiAt) / 3600; h > 0 {
		df.Lingqi = min(df.lingqiMax(), df.Lingqi+int32(h)*df.lingmai())
		df.LingqiAt += h * 3600
	}
}

// title 是贡献对应的称号（0–5）。
func dfTitle(contri int32) int {
	t := 0
	for _, need := range dfTitleExp {
		if contri < need {
			break
		}
		contri -= need
		t++
	}
	return t
}

// syncDF 刷新玩家的建筑上限快照，并在当天第一次时从灵气池领称号灵气。调用方持有洞府锁。
func (p *Player) syncDF(df *Dongfu, me *DFMember) {
	d := p.df()
	if df == nil {
		d.Caps = nil
		return
	}
	d.Caps = map[string]int{}
	for _, t := range dfXiuweiTypes {
		d.Caps[t] = df.build(t).Lv
	}
	df.accrueLingqi()
	if d.LingqiDay != today() && me != nil {
		d.LingqiDay = today()
		n := min(dfTitleLingqi[dfTitle(me.Contri)], df.Lingqi)
		df.Lingqi -= n
		d.Lingqi += n
	}
}

// effLv 是修为 t 的生效等级。
func (p *Player) effLv(t string, grade int) int {
	d := p.df()
	lv, _ := xiuweiLv(d.Exp[t])
	return min(lv, d.Caps[t], memberLimit(grade))
}

// dfPanelPct 是攻击 / 气血修为的面板加成百分比（效果文案「每一级提升全体 1% 面板攻击力 / 生命值」）。
// 面板值就是服务端下发的 petStats（PetCard:refreshWidgets 显示 data.att/data.hp），所以加成在 petStats 里算。
func (p *Player) dfPanelPct() (att, hp int) {
	if p.DF == nil || len(p.DF.Caps) == 0 {
		return 0, 0
	}
	g := p.grade()
	return p.effLv("attack", g), p.effLv("hp", g)
}

// pushXiuweiPanel 更新客户端缓存的护法属性，仅在生效加成变化时推送。
func (s *Session) pushXiuweiPanel(p *Player, oldAtt, oldHP int) {
	if att, hp := p.dfPanelPct(); att == oldAtt && hp == oldHP {
		return
	}
	pets := p.sortedPets()
	full := make([]xym.Msg, 0, len(pets))
	for _, pet := range pets {
		full = append(full, p.petFull(pet))
	}
	s.sendUpdates("PET_PTO.S_UPDATE_PET", "pets", full)
}

// applyXiuwei 把生效的修为战斗效果加到出战单位上（playerTeam 调用）：
// 气血修为的几率免疫道属性伤害（DaoImmune）、各族修为的属性减伤 / 对族增伤。
// 攻击 / 生命的 1%/级是面板加成，已含在 petStats 里，这里不再重复加。
func (p *Player) applyXiuwei(f *battle.Fighter) {
	if p.DF == nil || len(p.DF.Caps) == 0 {
		return
	}
	g := p.grade()
	if n := p.effLv("hp", g); n > 0 {
		f.DaoImmune = n
	}
	for t, rr := range dfRaceDmg {
		n := p.effLv(t, g)
		if n <= 0 {
			continue
		}
		f.ElemRes[dfElem[t]] += n
		if f.Race == rr[0] {
			f.RaceDmg[rr[1]] += n
		}
	}
}

func (p *Player) dfXiuweiList() xym.Msg {
	d := p.df()
	g := p.grade()
	list := []xym.Msg{}
	for _, t := range dfXiuweiTypes {
		lv, exp := xiuweiLv(d.Exp[t])
		next := xiuweiNeed(lv)
		if lv >= memberLimit(g) {
			next = 0 // 客户端 exp_next 0 显示「满级」
		}
		list = append(list, xym.Msg{"type": t, "lv": lv, "effect_lv": p.effLv(t, g), "curr_exp": d.Exp[t], "exp": exp,
			"exp_next": next, "avail_count": max(0, dfXiuDaily-d.Used[t])})
	}
	return xym.Msg{"list": list}
}

func (df *Dongfu) welfareMsg(p *Player) xym.Msg {
	list := []xym.Msg{}
	for _, t := range dfXiuweiTypes {
		b := df.build(t)
		next := xiuweiNeed(b.Lv)
		if b.Lv >= df.buildCap() {
			next = 0
		}
		list = append(list, xym.Msg{"type": t, "lv": b.Lv, "exp": b.Exp, "exp_next": next})
	}
	d := p.df()
	return xym.Msg{"xiuwei_list": list, "current_xiuwei": df.curXiuwei(), "lingqi_pool_lv": df.poolLv(),
		"house_lv": df.houseLv(), "lingqi": d.Lingqi, "god_soul": d.GodSoul}
}

func (df *Dongfu) houseLv() int32 { return max(df.HouseLv, 1) }
func (df *Dongfu) poolLv() int32  { return max(df.PoolLv, 1) }

// ---- 建设任务 ----

// dfTask 给玩家今天的建设任务（上交一件 1–3 星装备或法术书），返回剩余次数。
func (p *Player) dfTask(df *Dongfu, rng *rand.Rand) int {
	d := p.df()
	if d.TaskDay != today() {
		d.TaskDay, d.TaskDone, d.TaskKind, d.TaskType = today(), 0, "", 0
	}
	left := max(0, 1+df.extraToday()-d.TaskDone)
	if left > 0 && d.TaskType == 0 {
		star := 1 + rng.IntN(3)
		if rng.IntN(2) == 0 {
			var l []int // 过滤强化材料（2001-2004），上交需求只应是真装备
			for _, id := range gamedata.SuitsByStar[star] {
				if !isSuitMaterial(id) {
					l = append(l, id)
				}
			}
			if len(l) > 0 {
				d.TaskKind, d.TaskType = "suit", int32(l[rng.IntN(len(l))])
			}
		} else {
			var l []int32 // 过滤法术强化材料（17000-17004）
			for _, id := range gamedata.BooksByStar[star] {
				if !isBookMaterial(id) {
					l = append(l, id)
				}
			}
			if len(l) > 0 {
				d.TaskKind, d.TaskType = "magic", l[rng.IntN(len(l))]
			}
		}
	}
	return left
}

func (df *Dongfu) extraToday() int {
	if df.ExtraDay != today() {
		return 0
	}
	return df.Extra
}

func (s *Session) dfJobInfo(p *Player, df *Dongfu, me *DFMember) xym.Msg {
	m := dfJobInfo(me)
	if left := p.dfTask(df, s.actRng()); left > 0 && p.df().TaskType != 0 {
		m["xiuwei_task_type"], m["xiuwei_task_left_times"] = 1, left
		m["xiuwei_task_need_type"], m["xiuwei_task_need_kind"] = p.df().TaskType, p.df().TaskKind
	}
	s.srv.invadeJob(df, m)
	s.srv.gankJob(df, m)
	s.srv.caveJob(df, p, m)
	return m
}

// dfIgnoreGold：建设任务不交材料、直接用元宝完成的价格（客户端确认框写的 50）。
const dfIgnoreGold = 50

func onDongfuXiuweiTask(s *Session, p *Player, m xym.Msg) {
	item, kind := int32(m["item"].(int64)), m["kind"].(string)
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		d := p.df()
		left := p.dfTask(df, s.actRng())
		reply := func() { s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me)) }
		if left <= 0 || kind != d.TaskKind && kind != "ignore" {
			s.sendMsg("今天的建设任务已经完成了。")
			reply()
			return false
		}
		switch kind {
		case "suit":
			su := p.suitByNum(item)
			if su == nil || int32(su.Type) != d.TaskType || su.Grade > 1 || su.Locked || p.suitWorn(su.Pos) {
				s.sendMsg("请上交一件未穿戴、未强化的指定装备。")
				reply()
				return false
			}
			p.removeSuit(su)
			s.SendExt("ITEM_PTO.S_REMOVE_SUIT", xym.Msg{"suits": []xym.Msg{{"itemnum": su.ItemNum}}})
		case "magic":
			b := p.bookByPos(int(item))
			if b == nil || b.Type != d.TaskType || b.Grade > 1 || b.Locked || p.bookWorn(b.Pos) {
				s.sendMsg("请上交一本未穿戴、未强化的指定法术书。")
				reply()
				return false
			}
			p.removeBook(b)
			s.SendExt("ITEM_PTO.S_REMOVE_BOOK", xym.Msg{"books": []xym.Msg{{"pos": b.Pos}}})
		case "ignore": // 「确定花费50元宝忽略该材料?」（PartyView CPartyGetBuyView）：不交材料，花元宝直接完成
			if !s.spend(0, dfIgnoreGold) {
				reply()
				return false
			}
		default:
			reply()
			return false
		}
		d.TaskDone++
		d.TaskType, d.TaskKind = 0, ""
		df.addBuildExp(df.curXiuwei(), dfTaskBuildExp)
		df.Yuanqi += dfTaskYuanqi
		me.contribute(dfTaskContri)
		p.track(evDongfu, 1)
		p.syncDF(df, me)
		p.saveLog(s)
		s.sendMsg("建设任务完成，建设目标经验 +" + dfNum(dfTaskBuildExp) + "，贡献 +" + dfNum(dfTaskContri) + "。")
		reply()
		return true
	})
}

// ---- 修炼 ----

func onAddMemberXiuwei(s *Session, p *Player, m xym.Msg) {
	t, count, need := m["type"].(string), int(m["count"].(int64)), int(m["need_type"].(int64))
	oldAtt, oldHP := p.dfPanelPct()
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		p.syncDF(df, me)
		d := p.df()
		reply := func() {
			s.SendExt("DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST", p.dfXiuweiList())
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_WELFARE_INFO", df.welfareMsg(p))
		}
		if !slices.Contains(dfXiuweiTypes, t) || count < 1 {
			reply()
			return false
		}
		count = min(count, dfXiuDaily-d.Used[t])
		lv, _ := xiuweiLv(d.Exp[t])
		if count <= 0 || lv >= memberLimit(s.role.Grade) {
			s.sendMsg("今天的修炼次数已用完或修为已达上限。")
			reply()
			return false
		}
		// 逐次算花费（等级跨过 20 时单价变化）
		var gold, cash, lingqi, soul, godSoul int32
		exp := d.Exp[t]
		for range count {
			l, _ := xiuweiLv(exp)
			high := l >= dfXiuThreshold
			if need == 1 {
				gold += map[bool]int32{false: 10, true: 15}[high]
			} else {
				cash += map[bool]int32{false: 30000, true: 50000}[high]
				lingqi += map[bool]int32{false: 1, true: 2}[high]
			}
			soul += 10
			if high {
				godSoul++
			}
			exp += dfXiuExpUnit
		}
		switch {
		case lingqi > d.Lingqi:
			s.sendMsg("灵气不足。")
			reply()
			return false
		case godSoul > d.GodSoul:
			s.sendMsg("神魂不足。")
			reply()
			return false
		case s.role.SoulExp < soul:
			s.sendMsg("魂值不足。")
			reply()
			return false
		}
		if !s.spend(cash, gold) {
			reply()
			return false
		}
		s.roleUpdate(func(ro *Role) bool { ro.SoulExp -= soul; return true }, "soulexp")
		d.Lingqi -= lingqi
		d.GodSoul -= godSoul
		d.Exp[t] = exp
		d.Used[t] += count
		df.Yuanqi += int32(count)
		p.saveLog(s)
		reply()
		return true
	})
	s.pushXiuweiPanel(p, oldAtt, oldHP)
}

// ---- 建设目标、升级、扩建、额外任务 ----

func onDongfuCurXiuwei(s *Session, p *Player, m xym.Msg) {
	t := m["xiuwei"].(string)
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		if !isAdmin(me.Position) || !slices.Contains(dfXiuweiTypes, t) {
			s.sendMsg("只有府主和洞主可以设定建设目标。")
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_WELFARE_INFO", df.welfareMsg(p))
			return false
		}
		changed := df.curXiuwei() != t
		df.CurXiuwei = t
		if changed {
			s.srv.pushDongfuEvent(df, dfPushTask, "本洞府发布了新的建设目标，可前往「差事」查看。")
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_WELFARE_INFO", df.welfareMsg(p))
		return true
	})
}

func onUpgradeDongfu(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		lv := df.Lv
		need := 20 * lv
		if lv == 4 {
			need = 100
		}
		now := nowUnix()
		df.checkInvadeTimeout(now)
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以升级洞府。")
		case lv >= dfMaxLv:
			s.sendMsg("洞府已经是最高等级。")
		case int32(df.xiuweiSum()) < need:
			s.sendMsg("洞府修为不足。")
		case df.Gold < 500*lv:
			s.sendMsg("洞府元宝不足。")
		case df.Yuanqi < 200*lv:
			s.sendMsg("洞府元气不足。")
		case df.Invade != nil && (df.Invade.status(now) == 0 || df.Invade.status(now) == 4):
			s.sendMsg("当前已有正在进行的魔潮入侵，请先击退魔潮！")
		default:
			df.Gold -= 500 * lv
			df.Yuanqi -= 200 * lv
			df.startInvade(int(lv), time.Now().Hour(), now, true)
			df.log(s.role.Name + " 发起洞府升级，触发了魔潮入侵")
			s.srv.pushDongfuEvent(df, dfPushUpgrade, "本洞府发起升级，请在30分钟内击退魔潮。")
			s.sendMsg("提升洞府等级触发了［魔潮入侵］，30分钟内成功击退魔潮即可完成升级！")
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me))
			return true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_INFO", s.srv.dfInfoMsg(df, me))
		return false
	})
}

func (s *Session) dfExpand(house bool) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		lv := df.poolLv()
		if house {
			lv = df.houseLv()
		}
		cost := 300 * lv
		ok := false
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以扩建。")
		case lv >= df.Lv:
			s.sendMsg("扩建等级不能超过洞府等级。")
		case df.Gold < cost:
			s.sendMsg("洞府元宝不足，需要 " + dfNum(cost) + "。")
		default:
			df.Gold -= cost
			if house {
				df.HouseLv = lv + 1
			} else {
				df.PoolLv = lv + 1
			}
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_EXPAND_INFO", xym.Msg{"house_lv": df.houseLv(), "lingqi_pool_lv": df.poolLv()})
		return ok
	})
}

func (df *Dongfu) extraMsg() xym.Msg {
	return xym.Msg{"left_times": dfMaxExtraTasks - df.extraToday(), "gold": df.Gold, "yuanqi": df.Yuanqi}
}

func onAddExtraTask(s *Session, p *Player, m xym.Msg) {
	s.dfMine(func(df *Dongfu, me *DFMember) bool {
		cost := 100 * df.Lv
		ok := false
		switch {
		case !isAdmin(me.Position):
			s.sendMsg("只有府主和洞主可以发布建设任务。")
		case df.extraToday() >= dfMaxExtraTasks:
			s.sendMsg("今天不能再发布了。")
		case df.Gold < cost || df.Yuanqi < cost:
			s.sendMsg("洞府元宝或元气不足。")
		default:
			df.Gold -= cost
			df.Yuanqi -= cost
			if df.ExtraDay != today() {
				df.ExtraDay, df.Extra = today(), 0
			}
			df.Extra++
			df.log(s.role.Name + " 发布了额外的建设任务")
			ok = true
		}
		s.SendExt("DONGFU_PTO.S_SEND_DONGFU_EXTRA_XIUWEI_TASK_INFO", df.extraMsg())
		return ok
	})
}

func init() {
	Handle("DONGFU_PTO.C_ADD_MEMBER_XIUWEI", onAddMemberXiuwei)
	Handle("DONGFU_PTO.C_DONGFU_JOB_XIUWEI_TASK", onDongfuXiuweiTask)
	Handle("DONGFU_PTO.C_MODIFY_DONGFU_CURRENT_XIUWEI", onDongfuCurXiuwei)
	Handle("DONGFU_PTO.C_UPGRADE_DONGFU", onUpgradeDongfu)
	Handle("DONGFU_PTO.C_UPGRADE_DONGFU_HOUSE", func(s *Session, p *Player, m xym.Msg) { s.dfExpand(true) })
	Handle("DONGFU_PTO.C_UPGRADE_DONGFU_LINGQI_POOL", func(s *Session, p *Player, m xym.Msg) { s.dfExpand(false) })
	Handle("DONGFU_PTO.C_DONGFU_EXPAND_INFO", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_EXPAND_INFO", xym.Msg{"house_lv": df.houseLv(), "lingqi_pool_lv": df.poolLv()})
			return false
		})
	})
	Handle("DONGFU_PTO.C_ADD_EXTRA_XIUWEI_TASK_TIMES", onAddExtraTask)
	Handle("DONGFU_PTO.C_DONGFU_EXTRA_XIUWEI_TASK_INFO", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_EXTRA_XIUWEI_TASK_INFO", df.extraMsg())
			return false
		})
	})
	Handle("DONGFU_PTO.C_GET_DONGFU_JOB_INFO", func(s *Session, p *Player, m xym.Msg) {
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO", s.dfJobInfo(p, df, me))
			p.saveLog(s)
			return true
		})
	})
	Handle("DONGFU_PTO.C_GET_DONGFU_WELFARE_INFO", func(s *Session, p *Player, m xym.Msg) {
		oldAtt, oldHP := p.dfPanelPct()
		s.dfMine(func(df *Dongfu, me *DFMember) bool {
			p.syncDF(df, me)
			p.saveLog(s)
			s.SendExt("DONGFU_PTO.S_SEND_DONGFU_WELFARE_INFO", df.welfareMsg(p))
			return true
		})
		s.pushXiuweiPanel(p, oldAtt, oldHP)
	})
	Handle("DONGFU_PTO.C_MEMBER_XIUWEI_LIST", func(s *Session, p *Player, m xym.Msg) {
		oldAtt, oldHP := p.dfPanelPct()
		s.srv.dongfu.do(func() bool {
			df, me := s.srv.dongfu.of(s.role.UID)
			p.syncDF(df, me)
			return df != nil
		})
		p.saveLog(s)
		s.SendExt("DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST", p.dfXiuweiList())
		s.pushXiuweiPanel(p, oldAtt, oldHP)
	})
}
