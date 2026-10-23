package xymsrv

import (
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 背包：道具（Items，按格堆叠）、装备（Suits，每件一个 itemnum）、法术书（Books）。
//
// 阵容 Slot.Suit = [武器, 衣服, 鞋] 的装备背包位，Slot.Book = [书 1, 书 2] 的法术书背包位
// （抓包核对：spos1 = 武器、spos2 = 衣服、spos3 = 鞋；S_WEAR_SUIT 用 itemnum，S_WEAR_BOOK 用背包位）。

// Suit 是一件装备。
type Suit struct {
	ItemNum int32   `json:"itemnum"`
	Pos     int     `json:"pos"`
	Type    int     `json:"type"`
	Grade   int     `json:"grade"`
	Exp     int32   `json:"exp,omitempty"`
	Locked  bool    `json:"locked,omitempty"`
	Stone   int     `json:"stone,omitempty"` // 镶嵌的宝石道具号（inlay.go）
	Skill   int32   `json:"skill,omitempty"` // 炼神效果（神通号）
	Saved   []int32 `json:"saved,omitempty"` // 备用炼神效果
	shown   int32   // 显示用的炼神效果（套装激活后宝石提升的等级，inlay.go refreshShownSkills；不存档）
}

// Book 是一本法术书（类型即法术号）。
type Book struct {
	Pos    int   `json:"pos"`
	Type   int32 `json:"type"`
	Grade  int   `json:"grade"`
	Exp    int32 `json:"exp,omitempty"`
	Locked bool  `json:"locked,omitempty"`
	JX     int   `json:"jx,omitempty"` // 精修次数（jingxiu.go）
}

const (
	firstItemNum = 2001 // 装备 itemnum 起点（抓包里最小的是 2005）
	bagPet       = 1    // C_APPLY_BAG_COUNT 的 type：1 护法 2 装备 3 法术
	bagSuit      = 2
	bagBook      = 3
)

var suitSlot = map[string]int{"ITK_WEAPON": 0, "ITK_SHIRT": 1, "ITK_SHOE": 2}

func (s *Suit) msg() xym.Msg {
	return xym.Msg{"itemnum": s.ItemNum, "type": s.Type, "grade": s.Grade, "exp": s.Exp, "pos": s.Pos,
		"extra_exp": baseExp[suitStar(s.Type)], "is_lock": s.Locked, "skill": s.shownSkill(), "stone": s.Stone}
}

func (s *Suit) shownSkill() int32 {
	if s.shown != 0 {
		return s.shown
	}
	return s.Skill
}

func (b *Book) msg() xym.Msg {
	return xym.Msg{"pos": b.Pos, "type": b.Type, "grade": b.Grade, "jxgrade": b.JX, "exp": b.Exp,
		"extra_magic_exp": baseExp[magicStar(b.Type)], "is_lock": b.Locked}
}

func (it *Item) msg() xym.Msg {
	return xym.Msg{"pos": it.Pos, "type": it.Type, "amount": it.Amount, "expire": it.Expire}
}

// freePos 返回 used 里没有的最小正整数。
func freePos(used map[int]bool) int {
	pos := 1
	for used[pos] {
		pos++
	}
	return pos
}

// addItem 把 n 个 typ 放进背包：先叠到已有的格子，满了再开新格。返回动过的格子。
func (p *Player) addItem(typ int, n int32) []*Item {
	limit := int32(30000)
	if ii := gamedata.Items[typ]; ii != nil && ii.Overlap > 0 {
		limit = ii.Overlap
	}
	var touched []*Item
	for _, it := range p.Items {
		if n == 0 {
			break
		}
		if it.Type == typ && it.Amount < limit {
			add := min(n, limit-it.Amount)
			it.Amount += add
			n -= add
			touched = append(touched, it)
		}
	}
	for n > 0 {
		used := map[int]bool{}
		for _, it := range p.Items {
			used[it.Pos] = true
		}
		it := &Item{Pos: freePos(used), Type: typ, Amount: min(n, limit)}
		n -= it.Amount
		p.Items = append(p.Items, it)
		touched = append(touched, it)
	}
	return touched
}

// takeItems 从各格扣掉 n 个 typ（不够返回 nil 且不扣）；数量变 0 的格子从背包移除。
func (p *Player) takeItems(typ int, n int32) (changed, removed []*Item) {
	if p.itemCount(typ) < n {
		return nil, nil
	}
	kept := p.Items[:0]
	for _, it := range p.Items {
		if it.Type == typ && n > 0 {
			take := min(n, it.Amount)
			it.Amount -= take
			n -= take
			if it.Amount == 0 {
				removed = append(removed, it)
				continue
			}
			changed = append(changed, it)
		}
		kept = append(kept, it)
	}
	p.Items = kept
	return changed, removed
}

func (p *Player) itemByPos(pos int) *Item {
	for _, it := range p.Items {
		if it.Pos == pos {
			return it
		}
	}
	return nil
}

func (p *Player) addSuit(typ int) *Suit {
	used := map[int]bool{}
	for _, s := range p.Suits {
		used[s.Pos] = true
	}
	if p.NextItemNum < firstItemNum {
		p.NextItemNum = firstItemNum
	}
	s := &Suit{ItemNum: p.NextItemNum, Pos: freePos(used), Type: typ, Grade: 1}
	p.NextItemNum++
	p.Suits = append(p.Suits, s)
	return s
}

func (p *Player) addBook(typ int32) *Book {
	used := map[int]bool{}
	for _, b := range p.Books {
		used[b.Pos] = true
	}
	b := &Book{Pos: freePos(used), Type: typ, Grade: 1}
	p.Books = append(p.Books, b)
	return b
}

func (p *Player) suitByNum(num int32) *Suit {
	for _, s := range p.Suits {
		if s.ItemNum == num {
			return s
		}
	}
	return nil
}

func (p *Player) suitByPos(pos int) *Suit {
	for _, s := range p.Suits {
		if pos != 0 && s.Pos == pos {
			return s
		}
	}
	return nil
}

func (p *Player) bookByPos(pos int) *Book {
	for _, b := range p.Books {
		if pos != 0 && b.Pos == pos {
			return b
		}
	}
	return nil
}

// unrefGear 把所有阵容里对装备背包位 suitPos / 法术书背包位 bookPos 的引用清掉（0 表示不清）。
// 装备 / 法术书移除后背包位会被新东西复用（freePos），不清的话别的阵容那一栏就「变成」了新拿到的装备
// （出现过鞋子栏里是衣服 / 武器）。
func (p *Player) unrefGear(suitPos, bookPos int) {
	for _, lu := range p.Lineups {
		for _, sl := range lu.Slots {
			if sl == nil {
				continue
			}
			for i := range sl.Suit {
				if suitPos != 0 && sl.Suit[i] == suitPos {
					sl.Suit[i] = 0
				}
			}
			for i := range sl.Book {
				if bookPos != 0 && sl.Book[i] == bookPos {
					sl.Book[i] = 0
				}
			}
		}
	}
}

// fixSlotGear 让一个阵容位的装备按部位归位（武器 / 衣服 / 鞋），丢掉不存在的装备与法术书引用。
// 返回是否有改动。
func (p *Player) fixSlotGear(sl *Slot) bool {
	if sl == nil {
		return false
	}
	var suits [3]int
	for _, pos := range sl.Suit {
		su := p.suitByPos(pos)
		if su == nil || gamedata.Suits[su.Type] == nil {
			continue
		}
		if k := suitSlot[gamedata.Suits[su.Type].Kind]; suits[k] == 0 {
			suits[k] = pos
		}
	}
	var books [2]int
	n := 0
	for _, pos := range sl.Book {
		if pos != 0 && p.bookByPos(pos) != nil && n < 2 && (n == 0 || books[0] != pos) {
			books[n] = pos
			n++
		}
	}
	changed := suits != sl.Suit || books != sl.Book
	sl.Suit, sl.Book = suits, books
	return changed
}

// fixAllGear 整理所有阵容的装备引用（登录时修旧存档里错位 / 失效的引用）。
func (p *Player) fixAllGear() bool {
	changed := false
	for _, lu := range p.Lineups {
		for _, sl := range lu.Slots {
			if p.fixSlotGear(sl) {
				changed = true
			}
		}
	}
	return changed
}

// wearer 返回穿着该装备 / 法术书的阵容位（任一阵容），没穿返回 nil。
func (p *Player) suitWorn(pos int) bool {
	for _, lu := range p.Lineups {
		for _, sl := range lu.Slots {
			if sl != nil && pos != 0 && (sl.Suit[0] == pos || sl.Suit[1] == pos || sl.Suit[2] == pos) {
				return true
			}
		}
	}
	return false
}

func (p *Player) bookWorn(pos int) bool {
	for _, lu := range p.Lineups {
		for _, sl := range lu.Slots {
			if sl != nil && pos != 0 && (sl.Book[0] == pos || sl.Book[1] == pos) {
				return true
			}
		}
	}
	return false
}

// fightSlot 返回出战阵容的第 fp 位。
func (p *Player) fightSlot(fp int) *Slot {
	lu := p.lineup(p.FightNo)
	if lu == nil || fp < 1 || fp > len(lu.Slots) {
		return nil
	}
	return lu.Slots[fp-1]
}

// gearBonus 是出战阵容第 fp 位所穿装备的攻击 / 生命加成。
func (p *Player) gearBonus(fp int) (att, hp int32) {
	sl := p.fightSlot(fp)
	if sl == nil {
		return 0, 0
	}
	for _, pos := range sl.Suit {
		s := p.suitByPos(pos)
		if s == nil {
			continue
		}
		if si := gamedata.Suits[s.Type]; si != nil {
			b := si.Bonus(s.Grade)
			b += p.tjBonus(s.Type, b) // 天将：每阶 +10%
			switch si.Prop {
			case "att":
				att += b
			case "hp":
				hp += b
			}
		}
	}
	return att, hp
}

// wearSuitMsg / wearBookMsg：S_WEAR_SUIT / S_WEAR_BOOK 的全量（出战阵容 8 个位置）。
func (p *Player) wearSuitMsg() xym.Msg {
	var wear []xym.Msg
	for fp := 1; fp <= lineupSlots; fp++ {
		item := func(i int) int32 {
			if sl := p.fightSlot(fp); sl != nil {
				if s := p.suitByPos(sl.Suit[i]); s != nil {
					return s.ItemNum
				}
			}
			return 0
		}
		wear = append(wear, xym.Msg{"pos": fp, "weapon_item": item(0), "shirt_item": item(1), "shoe_item": item(2)})
	}
	return xym.Msg{"wear": wear}
}

func (p *Player) wearBookMsg() xym.Msg {
	var wear []xym.Msg
	for fp := 1; fp <= lineupSlots; fp++ {
		var b1, b2 int
		if sl := p.fightSlot(fp); sl != nil {
			b1, b2 = sl.Book[0], sl.Book[1]
		}
		wear = append(wear, xym.Msg{"pos": fp, "book1": b1, "book2": b2})
	}
	return xym.Msg{"wear": wear}
}

// ---- 掉落 ----

// gain 是发到手的一样东西（S_REWARD_MSG.list 的一项）。
type gain struct {
	rtype int // config.lua REWARD_TYPE_*：1 卡牌 3 物品 8 法术 10 装备
	typ   int
	count int32
}

const (
	rtCard      = 1
	rtSoul      = 2
	rtItem      = 3
	rtMagicPart = 4
	rtSuitPart  = 9
	rtMagic     = 8
	rtEquip     = 10
)

// bagDelta 记录一次发放 / 消耗引起的背包变化，用来回包。
type bagDelta struct {
	items     []*Item
	removed   []*Item
	suits     []*Suit
	books     []*Book
	pets      []*Pet
	souls     []int32 // 数量有变化的魂魄类型
	irons     []int   // 数量有变化的装备碎片类型
	bookParts []int32 // 数量有变化的法术碎片
	gains     []gain
	mailed    []string // 背包满了放不下、改用邮件寄出的奖励编码（ZB / FS / DZ + 编号），sendDelta 统一寄
}

var dropRe = regexp.MustCompile(`^([A-Z]+)(\d*)$`)

// rollDrops 按 logic/reward_mgr.lua 的编码掷骰：每段「前缀+编号:概率(万分比):数量」。
// item 字段里各项独立判定；每个 ritemN 字段是一次互斥抽取（概率合计 ≤10000，余下为不掉）——
// 例如「全民宝箱福蛋」ritem1 的 5 项概率合计正好 10000，只会出其中一种数量。
func (p *Player) rollDrops(rw *gamedata.RewardInfo, rng *rand.Rand, d *bagDelta) {
	for i, field := range rw.Drops {
		entries := strings.Split(field, ",")
		if i == 0 { // item：独立
			for _, e := range entries {
				code, prob, cnt := parseDrop(e)
				if prob > 0 && rng.IntN(10000) < prob {
					p.giveCode(code, cnt, rng, d)
				}
			}
			continue
		}
		r := rng.IntN(10000)
		for _, e := range entries { // ritemN：互斥
			code, prob, cnt := parseDrop(e)
			if r -= prob; r < 0 {
				p.giveCode(code, cnt, rng, d)
				break
			}
		}
	}
}

func parseDrop(e string) (code string, prob int, cnt int32) {
	parts := strings.Split(strings.TrimSpace(e), ":")
	code, cnt = parts[0], 1
	if len(parts) > 1 {
		prob, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		if n, err := strconv.Atoi(parts[2]); err == nil && n > 0 {
			cnt = int32(n) // 「R」等非数字按 1
		}
	}
	return code, prob, cnt
}

// gemBase + 等级 = 宝石道具号（10141 一级太阳石 … 10199 九级黑玉石）。
var gemBase = []int{10140, 10150, 10160, 10170, 10180, 10190}

// giveCode 发一段编码（logic/reward_mgr.lua rewardAnalysis）：WP 物品、ZB 装备、FS 法术书、DZ 卡牌、
// HP 魂魄、JT 装备碎片、SP 法术碎片；GP<类型><星级> 为该星级随机一个（GPDZ / GPDYDZ / GPYBDZ… 都按
// 卡牌，GP…ZB 装备，GP…FS 法术，GPJT / GPSP / GPHP 随机一种的碎片 / 魂魄，GPXBS 随机一颗该等级宝石）。积分 JF 等
// 本服还没有对应系统，暂不发。
func (p *Player) giveCode(code string, cnt int32, rng *rand.Rand, d *bagDelta) {
	if strings.HasPrefix(code, "GP") {
		m := dropRe.FindStringSubmatch(code[2:])
		if m == nil || m[2] == "" {
			return
		}
		star, _ := strconv.Atoi(m[2])
		switch kind := m[1]; {
		case strings.HasSuffix(kind, "DZ"):
			if l := gamedata.PetsByStar[star]; len(l) > 0 {
				for range cnt {
					p.giveCard(l[rng.IntN(len(l))], d)
				}
			}
		case strings.HasSuffix(kind, "ZB"):
			if l := gamedata.SuitsByStar[star]; len(l) > 0 {
				for range cnt {
					p.giveSuit(l[rng.IntN(len(l))], d)
				}
			}
		case strings.HasSuffix(kind, "FS"):
			if l := gamedata.BooksByStar[star]; len(l) > 0 {
				for range cnt {
					p.giveBook(l[rng.IntN(len(l))], d)
				}
			}
		case strings.HasSuffix(kind, "JT"): // 该星级随机一种装备的碎片 cnt 个
			if l := gamedata.SuitsByStar[star]; len(l) > 0 {
				p.addIron(l[rng.IntN(len(l))], cnt, d)
			}
		case strings.HasSuffix(kind, "SP"):
			if l := gamedata.BooksByStar[star]; len(l) > 0 {
				p.addBookPart(l[rng.IntN(len(l))], cnt, d)
			}
		case strings.HasSuffix(kind, "HP"): // 含 DYHP（功德商店招魂符）
			if l := gamedata.PetsByStar[star]; len(l) > 0 {
				p.addSoul(l[rng.IntN(len(l))], cnt, d)
			}
		case kind == "XBS": // 宝石袋：随机一颗该等级的宝石（六种：太阳石、红玛瑙、光芒石、月亮石、神秘石、黑玉石）
			if star >= 1 && star <= 9 {
				for range cnt {
					id := gemBase[rng.IntN(len(gemBase))] + star
					if gamedata.Items[id] != nil {
						d.items = append(d.items, p.addItem(id, 1)...)
						d.gains = append(d.gains, gain{rtItem, id, 1})
					}
				}
			}
		}
		return
	}
	m := dropRe.FindStringSubmatch(code)
	if m == nil || m[2] == "" {
		return
	}
	id, _ := strconv.Atoi(m[2])
	switch m[1] {
	case "WP":
		if gamedata.Items[id] != nil {
			d.items = append(d.items, p.addItem(id, cnt)...)
			d.gains = append(d.gains, gain{rtItem, id, cnt})
		}
	case "ZB":
		if gamedata.Suits[id] != nil {
			for range cnt {
				p.giveSuit(id, d)
			}
		}
	case "FS":
		if gamedata.Magics[int32(id)] != nil {
			for range cnt {
				p.giveBook(int32(id), d)
			}
		}
	case "DZ":
		if gamedata.Pets[int32(id)] != nil {
			for range cnt {
				p.giveCard(int32(id), d)
			}
		}
	case "HP":
		if gamedata.Pets[int32(id)] != nil {
			p.addSoul(int32(id), cnt, d)
		}
	case "JT":
		if gamedata.Suits[id] != nil {
			p.addIron(id, cnt, d)
		}
	case "SP":
		if gamedata.Magics[int32(id)] != nil {
			p.addBookPart(int32(id), cnt, d)
		}
	}
}

// addSoul 改魂魄数量（n 可为负）。魂魄类型就是卡牌类型，存在 Player.Souls。
func (p *Player) addSoul(typ, n int32, d *bagDelta) {
	if p.Souls == nil {
		p.Souls = map[int32]int32{}
	}
	p.Souls[typ] += n
	if p.Souls[typ] <= 0 {
		delete(p.Souls, typ)
	}
	d.souls = append(d.souls, typ)
	if n > 0 {
		d.gains = append(d.gains, gain{rtSoul, int(typ), n})
	}
}

// soulsMsg 是 S_UPDATE_CARD：pet_mgr 按 type 合并，amount 为新的总数，0 表示删除。
func (p *Player) soulsMsg(types []int32) xym.Msg {
	seen := map[int32]bool{}
	l := []xym.Msg{}
	for _, t := range types {
		if !seen[t] {
			seen[t] = true
			l = append(l, xym.Msg{"type": t, "amount": p.Souls[t]})
		}
	}
	return xym.Msg{"cards": l}
}

// giveCard / giveSuit / giveBook：背包满了就改用邮件寄（d.mailed），奖励列表照样列出，免得东西丢了、
// 十连弹窗凑不满 10 格显示成「载入中」。同名卡并入不占格子。
func (p *Player) giveCard(typ int32, d *bagDelta) {
	if len(p.Pets) >= p.BagLimit[bagPet] && p.petByType(typ) == nil {
		d.mailed = append(d.mailed, "DZ"+strconv.Itoa(int(typ)))
		d.gains = append(d.gains, gain{rtCard, int(typ), 1})
		return
	}
	d.pets = append(d.pets, p.gainPet(typ)) // 已有同类护法就并进去（pet_talent.go）
	d.gains = append(d.gains, gain{rtCard, int(typ), 1})
}

func (p *Player) giveSuit(typ int, d *bagDelta) {
	if len(p.Suits) >= p.BagLimit[bagSuit] {
		d.mailed = append(d.mailed, "ZB"+strconv.Itoa(typ))
		d.gains = append(d.gains, gain{rtEquip, typ, 1})
		return
	}
	d.suits = append(d.suits, p.addSuit(typ))
	d.gains = append(d.gains, gain{rtEquip, typ, 1})
}

func (p *Player) giveBook(typ int32, d *bagDelta) {
	if len(p.Books) >= p.BagLimit[bagBook] {
		d.mailed = append(d.mailed, "FS"+strconv.Itoa(int(typ)))
		d.gains = append(d.gains, gain{rtMagic, int(typ), 1})
		return
	}
	d.books = append(d.books, p.addBook(typ))
	d.gains = append(d.gains, gain{rtMagic, int(typ), 1})
}

// sendDelta 把背包变化发给客户端，返回 S_REWARD_MSG.list。
func (s *Session) sendDelta(p *Player, d *bagDelta) []xym.Msg {
	// 先删后加：空出来的格子可能马上被新得的道具占用（开宝箱扣光宝箱钥匙再掉东西），
	// 反过来发客户端会把新道具删掉；已被重新占用的格子不再发删除。
	if len(d.removed) > 0 {
		var l []xym.Msg
		for _, it := range d.removed {
			if p.itemByPos(it.Pos) == nil {
				l = append(l, xym.Msg{"pos": it.Pos})
			}
		}
		if len(l) > 0 {
			s.SendExt("ITEM_PTO.S_REMOVE_ITEM", xym.Msg{"items": l})
		}
	}
	if len(d.items) > 0 {
		seen := map[*Item]bool{}
		var l []xym.Msg
		for _, it := range d.items {
			if !seen[it] && it.Amount > 0 {
				seen[it] = true
				l = append(l, it.msg())
			}
		}
		if len(l) > 0 {
			s.SendExt("ITEM_PTO.S_UPDATE_ITEM", xym.Msg{"is_all": 0, "items": l})
		}
	}
	// 通用碎片道具另有一张整表（装备 / 法术背包「碎片」页显示）。碎片页在收到
	// S_UPDATE_SUIT / BOOK、S_UPDATE_SUIT_IRON / BOOK_PART 时刷新，所以要先于它们发。
	suitPart, magicPart := false, false
	for _, it := range append(append([]*Item(nil), d.items...), d.removed...) {
		suitPart = suitPart || isPartItem(it.Type, suitPartItem)
		magicPart = magicPart || isPartItem(it.Type, magicPartItem)
	}
	if suitPart {
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT_PART_ITEMS", p.partItemsMsg(suitPartItem))
	}
	if magicPart {
		s.SendExt("ITEM_PTO.S_UPDATE_MAGIC_PART_ITEMS", p.partItemsMsg(magicPartItem))
	}
	if len(d.suits) > 0 {
		var l []xym.Msg
		for _, x := range d.suits {
			l = append(l, x.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT", xym.Msg{"is_all": 0, "suits": l})
	}
	if len(d.books) > 0 {
		var l []xym.Msg
		for _, x := range d.books {
			l = append(l, x.msg())
		}
		s.SendExt("ITEM_PTO.S_UPDATE_BOOK", xym.Msg{"is_all": 0, "books": l})
	}
	if len(d.pets) > 0 {
		var full, brief []xym.Msg
		for _, pet := range d.pets {
			full = append(full, p.petFull(pet))
			brief = append(brief, p.petBrief(pet))
		}
		s.SendExt("PET_PTO.S_UPDATE_PET", xym.Msg{"is_all": 0, "pets": full})
		s.SendExt("PET_PTO.S_PET_LIST", xym.Msg{"is_all": 0, "pets": brief})
	}
	if len(d.souls) > 0 {
		s.SendExt("PET_PTO.S_UPDATE_CARD", p.soulsMsg(d.souls))
	}
	if len(d.irons) > 0 {
		s.SendExt("ITEM_PTO.S_UPDATE_SUIT_IRON", p.ironsMsg(d.irons))
	}
	if len(d.bookParts) > 0 {
		s.SendExt("ITEM_PTO.S_UPDATE_BOOK_PART", p.bookPartsMsg(d.bookParts))
	}
	if len(d.mailed) > 0 {
		items := map[string]int32{}
		for _, code := range d.mailed {
			items[code]++
		}
		d.mailed = nil
		if _, err := s.srv.SendMail(s.role.UID, MailSpec{Title: "背包已满", Text: "背包已满，放不下的物品已通过邮件寄给你，清理背包后在邮件里领取。", Items: items}); err != nil {
			s.logErr("overflow mail", err)
		} else {
			s.retMsg("背包已满，放不下的物品已寄到邮件。")
		}
	}
	// 同类合并成一项（同一种物品多次掉落合计数量）
	merged := map[[2]int]int32{}
	var order [][2]int
	for _, g := range d.gains {
		k := [2]int{g.rtype, g.typ}
		if _, ok := merged[k]; !ok {
			order = append(order, k)
		}
		merged[k] += g.count
	}
	list := make([]xym.Msg, 0, len(order))
	for _, k := range order {
		list = append(list, xym.Msg{"rtype": k[0], "type": k[1], "count": merged[k]})
	}
	return list
}

func sortedItems(l []*Item) []*Item {
	out := append([]*Item(nil), l...)
	sort.Slice(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out
}
