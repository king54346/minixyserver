package xymsrv

import (
	"math/rand/v2"
	"time"

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
	code string // giveCode 编码
	cnt  int32
}

type boxKind struct {
	box              int // 宝箱的道具号（回包 item、送回的宝箱）
	drops            []boxDrop
	soulMin, soulMax int32 // 每箱魂值
	again            int   // 每箱额外送回一个宝箱的概率（万分比）
}

var boxKinds = map[int]*boxKind{
	// 黄金宝箱：权重 ×2 照抓包分布；描述「2-5星装备和法术」，抓包 50 箱没出 4、5 星装备和 4 星法术，各按抓包里最少的一档的一半补上（估计）。
	10002: {box: 10002, soulMin: 20, soulMax: 23, again: 800, drops: []boxDrop{
		{12, "GPFS2", 1}, {2, "GPFS3", 1}, {1, "GPFS4", 1}, {6, "GPFS5", 1},
		{16, "GPZB2", 1}, {4, "GPZB3", 1}, {1, "GPZB4", 1}, {6, "GPZB5", 1}, // 5 星装备与 5 星法术同权重（用户要求）
		{20, "WP10092", 5}, {16, "WP10093", 1}, {2, "WP10094", 1}, {6, "WP10095", 1}, {6, "WP10437", 1},
		{8, "GPSP5", 1}, {2, "GPJT5", 1}, {2, "WP10084", 1},
		{2, "ZB1507", 1}, {2, "ZB1508", 1}, // 紫鞋（4 星乾坤天罡履 / 七星逐月靴，原服「节日活动」，用户要求加入）
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

// boxOf：typ 是宝箱或钥匙时返回该宝箱的规则。
func boxOf(typ int) *boxKind {
	if bk := boxKinds[typ]; bk != nil {
		return bk
	}
	return boxKinds[boxPair[typ]]
}

// openBoxes 开 n 个宝箱（调用方已确认宝箱和钥匙各有 n 个）：扣宝箱和钥匙、发奖，返回魂值和送回的宝箱数。
// 每箱一项奖励的 gain 按顺序记在 d.gains（十连的 list 不合并，照抓包每箱一项）。
func (p *Player) openBoxes(bk *boxKind, n int32, rng *rand.Rand, d *bagDelta) (soul, again int32) {
	for _, t := range []int{bk.box, boxPair[bk.box]} {
		ch, rm := p.takeItems(t, n)
		d.items, d.removed = append(d.items, ch...), append(d.removed, rm...)
	}
	total := 0
	for _, x := range bk.drops {
		total += x.w
	}
	for range n {
		r := rng.IntN(total)
		for _, x := range bk.drops {
			if r -= x.w; r < 0 {
				p.giveCode(x.code, x.cnt, rng, d)
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
		bk := boxOf(typ)
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
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(p.UID)))
	d := &bagDelta{}
	soul, again := p.openBoxes(bk, n, rng, d)
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
	list := make([]xym.Msg, 0, len(gains))
	for _, g := range gains {
		list = append(list, xym.Msg{"rtype": g.rtype, "type": g.typ, "count": g.count})
	}
	bonus := []xym.Msg{}
	if again > 0 {
		bonus = append(bonus, xym.Msg{"rtype": rtItem, "type": bk.box, "count": again})
	}
	if n == 1 { // 单开：一条 BOX_ITEM（带「继续使用」）
		s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxItem, "item": typ, "soul": soul, "list": append(list, bonus...)})
		return
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxTen, "item": typ, "list": list})
	s.SendExt("ETC_PTO.S_REWARD_MSG", xym.Msg{"type": boxNormal, "item": bk.box, "soul": soul, "list": bonus})
}
