package xymsrv

import "turnrpg/internal/xym"

// materials 收集强化材料：装备 / 法术书（去重、排除目标、已穿戴和已锁定的）与整堆碎片。
type materials struct {
	suits []*Suit
	books []*Book
	irons []int
	parts []int32
	exp   int32
}

func (p *Player) suitMaterials(m xym.Msg, target *Suit) *materials {
	mt := &materials{}
	seen := map[*Suit]bool{target: true}
	reqs, _ := m["suits"].([]xym.Msg)
	var byNum map[int32]*Suit
	if len(reqs) > 0 {
		byNum = make(map[int32]*Suit, len(reqs))
		for _, r := range reqs {
			byNum[int32(r["itemnum"].(int64))] = nil
		}
		for _, x := range p.Suits {
			if _, wanted := byNum[x.ItemNum]; wanted {
				byNum[x.ItemNum] = x
			}
		}
	}
	for _, r := range reqs {
		x := byNum[int32(r["itemnum"].(int64))]
		if x == nil || seen[x] || x.Locked || p.suitWorn(x.Pos) {
			continue
		}
		seen[x] = true
		mt.suits = append(mt.suits, x)
		mt.exp += x.expValue()
	}
	used := map[int]bool{}
	irons, _ := m["suit_irons"].([]xym.Msg)
	for _, r := range irons {
		t := int(r["type"].(int64))
		if n := p.Irons[t]; n > 0 && !used[t] {
			used[t] = true
			mt.irons = append(mt.irons, t)
			mt.exp += partExp[suitStar(t)] * n
		}
	}
	return mt
}

func (p *Player) bookMaterials(m xym.Msg, target *Book) *materials {
	mt := &materials{}
	seen := map[*Book]bool{}
	if target != nil {
		seen[target] = true
	}
	reqs, _ := m["books"].([]xym.Msg)
	var byPos map[int]*Book
	if len(reqs) > 0 {
		byPos = make(map[int]*Book, len(reqs))
		for _, r := range reqs {
			byPos[int(r["pos"].(int64))] = nil
		}
		for _, x := range p.Books {
			if _, wanted := byPos[x.Pos]; wanted {
				byPos[x.Pos] = x
			}
		}
	}
	for _, r := range reqs {
		x := byPos[int(r["pos"].(int64))]
		if x == nil || seen[x] || x.Locked || p.bookWorn(x.Pos) {
			continue
		}
		seen[x] = true
		mt.books = append(mt.books, x)
		mt.exp += x.expValue()
	}
	used := map[int32]bool{}
	parts, _ := m["book_parts"].([]xym.Msg)
	for _, r := range parts {
		t := int32(r["type"].(int64))
		if n := p.BookParts[t]; n > 0 && !used[t] {
			used[t] = true
			mt.parts = append(mt.parts, t)
			mt.exp += partExp[magicStar(t)] * n
		}
	}
	return mt
}

// consume 删掉材料并发 S_REMOVE_SUIT / S_REMOVE_BOOK 和碎片变化。
func (s *Session) consume(p *Player, mt *materials) {
	d := &bagDelta{}
	if len(mt.suits) > 0 {
		var l []xym.Msg
		removed := make(map[*Suit]bool, len(mt.suits))
		for _, x := range mt.suits {
			removed[x] = true
			p.unrefGear(x.Pos, 0)
			l = append(l, xym.Msg{"itemnum": x.ItemNum})
		}
		kept := p.Suits[:0]
		for _, x := range p.Suits {
			if !removed[x] {
				kept = append(kept, x)
			}
		}
		clear(p.Suits[len(kept):])
		p.Suits = kept
		s.SendExt("ITEM_PTO.S_REMOVE_SUIT", xym.Msg{"suits": l})
	}
	if len(mt.books) > 0 {
		var l []xym.Msg
		removed := make(map[*Book]bool, len(mt.books))
		for _, x := range mt.books {
			removed[x] = true
			p.unrefGear(0, x.Pos)
			l = append(l, xym.Msg{"pos": x.Pos})
		}
		kept := p.Books[:0]
		for _, x := range p.Books {
			if !removed[x] {
				kept = append(kept, x)
			}
		}
		clear(p.Books[len(kept):])
		p.Books = kept
		s.SendExt("ITEM_PTO.S_REMOVE_BOOK", xym.Msg{"books": l})
	}
	for _, t := range mt.irons {
		p.addIron(t, -p.Irons[t], d)
	}
	for _, t := range mt.parts {
		p.addBookPart(t, -p.BookParts[t], d)
	}
	s.sendDelta(p, d)
}
