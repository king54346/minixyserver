package xymsrv

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"strconv"
	"strings"

	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 宝箱。客户端 view/ItemListView.lua 的 BoxId 里宝箱和钥匙互为「需要的另一件」：点哪一件的「使用」
// 都是开一次（C_USE_ITEM），「开十次」发 ETC_PTO.C_OPEN_TEN_BOX{type}，每开一次各扣一个宝箱和钥匙。
//
// 抓包（session-01 连开 5 次黄金十连，C_OPEN_TEN_BOX{type:10003}）每次回：
//   S_UPDATE_ITEM 等背包变化、S_ATTR_UPDATE{soulexp}、
//   S_REWARD_MSG{type:8 BOX_TEN, item:请求的 type, list:10 项}、
//   S_REWARD_MSG{type:1, item:10002, soul:213~225（每箱约 22 魂值）, list:偶尔 1~2 个黄金宝箱}。
// 10 项的分布（50 项合计）：法术书 10（2 星 6、3 星 1、5 星 3）、装备 10（2 星 8、3 星 2）、
// 小银锭×5 10、银锭 8、大银锭 1、三星筑基丹 3、《虎翼阵》3、5 星法术碎片 4、5 星装备碎片 1、五星装备碎片道具 1。
// 白银、黑铁没有抓包，按道具描述（白银「2-4 星」、
// 黑铁「1-3 星装备和法术、银两、魂值」）缩小定的，属于估计。

var boxPair = map[int]int{10002: 10003, 10003: 10002, 10004: 10005, 10005: 10004, 10006: 10007, 10007: 10006}

type boxDrop struct {
	w    int
	code string // giveCode 编码；ZFBOOK = 随机阵法书（10432~10439 完全平均）
	cnt  int32
}

// boxGradeLimit：某些掉落只对指定人物等级开放（空/不存在 = 所有等级都能抽）。
var boxGradeLimit = map[string][]int{
	"WP10015": {30, 40}, // 高级经验丹：只有 30 / 40 级角色能开出来
}

type boxKind struct {
	version          int64
	box              int // 宝箱的道具号（回包 item、送回的宝箱）
	drops            []boxDrop
	soulMin, soulMax int32 // 每箱魂值
	again            int   // 每箱额外送回一个宝箱的概率（万分比）
}

var boxKinds = map[int]*boxKind{
	// 黄金宝箱：权重 ×2 照抓包分布；描述「2-5星装备和法术」，抓包 50 箱没出 4、5 星装备和 4 星法术，各按抓包里最少的一档的一半补上（估计）。
	// 2026-10-30 用户要求：5 星（5 星法术 / 装备 / 法术碎片 / 装备碎片 / 五星装备碎片道具）权重减半；加金鞋。
	// 2026-11-02：所有掉落权重整体 ×9，总权重 1000（30/40 级，含高级经验丹）。
	// 金鞋两双合并成 ZFBOOT 一项、权重 1 → 金鞋总概率恰好 0.1%；两双再各 50% 随机。
	10002: {box: 10002, soulMin: 20, soulMax: 23, again: 800, drops: []boxDrop{
		{108, "GPFS2", 1}, {18, "GPFS3", 1}, {9, "GPFS4", 1}, {27, "GPFS5", 1},
		{144, "GPZB2", 1}, {36, "GPZB3", 1}, {9, "GPZB4", 1}, {27, "GPZB5", 1},
		{180, "WP10092", 5}, {144, "WP10093", 1}, {18, "WP10094", 1}, {45, "WP10095", 1}, {45, "WP10096", 1}, {36, "ZFBOOK", 1},
		{63, "WP10015", 1}, // 高级经验丹：仅人物等级 30 / 40（boxGradeLimit）
		{36, "GPSP5", 1}, {9, "GPJT5", 1}, {9, "WP10084", 1},
		{18, "ZB1507", 1}, {18, "ZB1508", 1}, // 紫鞋：各 1.8%
		{1, "ZFBOOT", 1}, // 金鞋（碧霞流云履 / 金丝逐日履随机）：合计 0.1%
	}},
	10004: {box: 10004, soulMin: 10, soulMax: 12, drops: []boxDrop{
		{6, "GPFS2", 1}, {3, "GPFS3", 1}, {1, "GPFS4", 1},
		{6, "GPZB2", 1}, {3, "GPZB3", 1}, {1, "GPZB4", 1},
		{12, "WP10092", 3}, {4, "WP10093", 1}, {3, "WP10095", 1},
		{2, "GPSP5", 1}, {1, "GPJT5", 1},
	}},
	10006: {box: 10006, soulMin: 5, soulMax: 6, drops: []boxDrop{
		{6, "GPFS1", 1}, {3, "GPFS2", 1}, {1, "GPFS3", 1},
		{6, "GPZB1", 1}, {3, "GPZB2", 1}, {1, "GPZB3", 1},
		{16, "WP10092", 1}, {4, "WP10092", 3},
		{1, "GPSP5", 1}, {1, "GPJT5", 1},
	}},
}

func boxOf(typ int) *boxKind {
	if bk := boxKinds[typ]; bk != nil {
		return bk
	}
	return boxKinds[boxPair[typ]]
}

// boxCodeName 把掉落编码翻成中文名，给 GM 页面显示（不暴露编码）。
func boxCodeName(code string) string {
	if strings.HasPrefix(code, "GP") {
		if m := dropRe.FindStringSubmatch(code[2:]); m != nil && len(m) > 2 && m[2] != "" {
			star, _ := strconv.Atoi(m[2])
			switch kind := m[1]; {
			case strings.HasSuffix(kind, "FS"):
				return fmt.Sprintf("%d星法术书", star)
			case strings.HasSuffix(kind, "ZB"):
				return fmt.Sprintf("%d星装备", star)
			case strings.HasSuffix(kind, "SP"):
				return fmt.Sprintf("%d星法术碎片", star)
			case strings.HasSuffix(kind, "JT"):
				return fmt.Sprintf("%d星装备碎片", star)
			case strings.HasSuffix(kind, "HP"):
				return fmt.Sprintf("%d星英魂", star)
			case strings.HasSuffix(kind, "DZ"):
				return fmt.Sprintf("%d星卡牌", star)
			}
		}
	}
	switch {
	case code == "ZFBOOK":
		return "随机阵法书"
	case code == "ZFBOOT":
		return "随机金鞋"
	case strings.HasPrefix(code, "WP"):
		if id, err := strconv.Atoi(code[2:]); err == nil {
			if ii := gamedata.Items[id]; ii != nil && ii.Name != "" {
				return ii.Name
			}
		}
	case strings.HasPrefix(code, "ZB"):
		if id, err := strconv.Atoi(code[2:]); err == nil {
			if si := gamedata.Suits[id]; si != nil && si.Name != "" {
				return si.Name
			}
		}
	}
	return code
}

// boxSnapshot 导出当前所有宝箱规则，供 GM 工具查看。
func (s *Server) boxSnapshot() map[int]map[string]any {
	out := make(map[int]map[string]any, len(boxKinds))
	for id := range boxKinds {
		bk := s.boxRule(id)
		drops := make([]map[string]any, 0, len(bk.drops))
		for _, d := range bk.drops {
			drops = append(drops, map[string]any{
				"w": d.w, "code": d.code, "name": boxCodeName(d.code), "cnt": d.cnt,
				"gradeOnly": boxGradeLimit[d.code], // 非空 = 只有这些人物等级能抽到
			})
		}
		out[id] = map[string]any{"box": bk.box, "soulMin": bk.soulMin, "soulMax": bk.soulMax, "again": bk.again, "drops": drops}
	}
	return out
}

// updateBoxDrops persists the same table used by the operations page and by opening boxes.
func (s *Server) updateBoxDrops(change func(map[int]BoxOdds)) error {
	if !s.cfg.EnableGMOps || s.ops == nil || s.ops.st == nil {
		return fmt.Errorf("GM 概率存档未启用")
	}
	settings := *s.ops.current.Load()
	settings.Drops.Boxes = maps.Clone(settings.Drops.Boxes)
	change(settings.Drops.Boxes)
	return s.ops.update(settings)
}

func (s *Server) boxReset(id int) error {
	presets := defaultOpsSettings().Drops.Boxes
	if id != 0 && boxKinds[id] == nil {
		return fmt.Errorf("宝箱 %d 不存在", id)
	}
	return s.updateBoxDrops(func(boxes map[int]BoxOdds) {
		for boxID, preset := range presets {
			if id == 0 || boxID == id {
				rule := boxes[boxID]
				rule.Drops = preset.Drops
				boxes[boxID] = rule
			}
		}
	})
}

func (s *Server) boxSet(id int, drops []boxDrop) error {
	if boxKinds[id] == nil || len(drops) == 0 || len(drops) > 100 {
		return fmt.Errorf("宝箱或掉落列表无效")
	}
	weights := make([]DropWeight, 0, len(drops))
	unrestricted := false
	for _, d := range drops {
		unrestricted = unrestricted || (d.w > 0 && len(boxGradeLimit[d.code]) == 0)
		weights = append(weights, DropWeight{Code: d.code, Count: d.cnt, Weight: d.w})
	}
	if !unrestricted {
		return fmt.Errorf("至少需要一项不限等级且权重大于 0 的掉落")
	}
	return s.updateBoxDrops(func(boxes map[int]BoxOdds) {
		rule := boxes[id]
		rule.Drops = weights
		boxes[id] = rule
	})
}

func validBoxRewardCode(code string) bool {
	return code == "ZFBOOK" || code == "ZFBOOT" || validOpsRewardCode(code)
}

func boxDropEligible(drop boxDrop, grade int) bool {
	if drop.w <= 0 {
		return false
	}
	grades := boxGradeLimit[drop.code]
	if len(grades) == 0 {
		return true
	}
	for _, allowed := range grades {
		if grade == allowed {
			return true
		}
	}
	return false
}

// openBoxes 开 n 个宝箱（调用方已确认宝箱和钥匙各有 n 个）：扣宝箱和钥匙、发奖，返回魂值和送回的宝箱数。
// 每箱一项奖励的 gain 按顺序记在 d.gains（十连的 list 不合并，照抓包每箱一项）。
func (p *Player) openBoxes(bk *boxKind, n int32, rng *rand.Rand, d *bagDelta) (soul, again int32) {
	for _, t := range []int{bk.box, boxPair[bk.box]} {
		ch, rm := p.takeItems(t, n)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	}
	total := 0
	grade := p.grade()
	for _, drop := range bk.drops {
		if boxDropEligible(drop, grade) {
			total += drop.w
		}
	}
	for range n {
		if total <= 0 {
			break
		}
		r := rng.IntN(total)
		for _, x := range bk.drops {
			if !boxDropEligible(x, grade) {
				continue
			}
			if r -= x.w; r < 0 {
				if x.code == "ZFBOOK" {
					p.giveCode("WP"+strconv.Itoa(10432+rng.IntN(8)), x.cnt, rng, d) // 8 本阵法书等概率
				} else if x.code == "ZFBOOT" {
					p.giveCode("ZB"+strconv.Itoa(1509+rng.IntN(2)), x.cnt, rng, d) // 两双金鞋各 50%
				} else {
					p.giveCode(x.code, x.cnt, rng, d)
				}
				break
			}
		}
		soul += bk.soulMin + rng.Int32N(bk.soulMax-bk.soulMin+1)
		if rng.IntN(10000) < bk.again {
			again++
		}
	}
	if again > 0 {
		d.items = append(d.items, p.addItem(bk.box, again)...)
	}
	return soul, again
}

// boxCount 是 typ 对应的宝箱、钥匙里较少的那个数量（能开几次）。
func (p *Player) boxCount(bk *boxKind) int32 {
	return min(p.itemCount(bk.box), p.itemCount(boxPair[bk.box]))
}

func init() {
	Handle("ETC_PTO.C_OPEN_TEN_BOX", func(s *Session, p *Player, m xym.Msg) {
		typ := int(m["type"].(int64))
		bk := s.srv.boxRule(typ)
		if bk == nil {
			s.sendMsg("这不是宝箱。")
			return
		}
		if p.boxCount(bk) < 10 {
			s.sendMsg("宝箱或钥匙不足 10 个。")
			return
		}
		s.openBoxReply(p, bk, typ, 10)
	})
}

// openBoxReply 开箱并回包（单开 n=1 用 BOX_ITEM，十连用 BOX_TEN，魂值和送回的宝箱另起一条）。
func (s *Session) openBoxReply(p *Player, bk *boxKind, typ int, n int32) {
	eligible := false
	for _, drop := range bk.drops {
		if boxDropEligible(drop, p.grade()) {
			eligible = true
			break
		}
	}
	if !eligible {
		s.sendMsg("当前等级没有可用的宝箱奖励。")
		return
	}

	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID)))
	d := &bagDelta{}
	soul, again := p.openBoxes(bk, n, rng, d)
	s.srv.opsResult("box_open", p.UID, bk.version, map[string]any{"box": bk.box, "count": n, "soul": soul, "returned_boxes": again, "rewards": d.auditGains()})
	p.saveLog(s)
	gains := d.gains
	d.gains = nil
	s.sendDelta(p, d)
	role, err := s.srv.roles.Update(s.role.UID, func(ro *Role) bool { ro.SoulExp += soul; return true })
	if err != nil {
		s.logErr("save role", err)
	} else {
		s.role = role
		s.SendExt("ETC_PTO.S_ATTR_UPDATE", xym.Msg{"attr": []xym.Msg{{"name": "soulexp", "val": role.SoulExp}}})
	}
	var list []xym.Msg
	if n > 10 {
		list = mergeRewardGains(gains)
	} else {
		list = make([]xym.Msg, 0, len(gains))
		for _, g := range gains {
			list = append(list, xym.Msg{"rtype": g.rtype, "type": g.typ, "count": g.count})
		}
	}
	bonus := []xym.Msg{}
	if again > 0 {
		bonus = append(bonus, xym.Msg{"rtype": rtItem, "type": bk.box, "count": again})
	}
	if n == 1 { // 单开：一条 BOX_ITEM（带「继续使用」）
		s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxItem, "item": typ, "soul": soul, "list": append(list, bonus...)})
		return
	}
	if n > 10 { // 大批量开箱合并同类奖励，避免按几千项创建十连动画/超长帧。
		s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxNormal, "item": typ, "soul": soul, "list": append(list, bonus...)})
		return
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxTen, "item": typ, "list": list})
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxNormal, "item": bk.box, "soul": soul, "list": bonus})
}
