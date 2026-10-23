package xymsrv

import (
	"math"
	"testing"

	"turnrpg/internal/gamedata"
)

func petTypeByName(t *testing.T, name string) int32 {
	t.Helper()
	for typ, pi := range gamedata.Pets {
		if pi.Name == name && pi.Star == 5 {
			return typ
		}
	}
	t.Fatalf("no 5-star pet %s", name)
	return 0
}

// TestRelate：缘分倍率照 client pet_prop.getPetRelateRatio。东华帝君的缘分：
// 2407 神农尺(金) / 晓风残月(紫) hp 15 / 8；2408 郁金流苏簪 / 蝉翼金丝甲 att 15 / 8；
// 2409 大鹏展翅 / 太玄神功（法术书）hp 10 / 5；2410 太上老君同阵 att 10。
func TestRelate(t *testing.T) {
	p := newPlayer(1)
	pet := p.addPet(35042)
	lu := p.lineup(p.FightNo)
	for _, sl := range lu.Slots {
		sl.Pet = 0
	}
	sl := lu.Slots[0]
	sl.Pet = pet.BagPos
	pi := gamedata.Pets[35042]
	near := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s: ratio %.4f, want %.4f", name, got, want)
		}
	}
	near("none att", p.relateRatio(pi, 1, false), 1)
	near("none hp", p.relateRatio(pi, 1, true), 1)

	gold, purple := p.addSuit(1040), p.addSuit(1014)
	sl.Suit[suitSlot["ITK_WEAPON"]] = purple.Pos
	near("紫武器 hp", p.relateRatio(pi, 1, true), 1.08)
	sl.Suit[suitSlot["ITK_WEAPON"]] = gold.Pos
	near("金武器 hp", p.relateRatio(pi, 1, true), 1.15)

	sl.Suit[suitSlot["ITK_SHIRT"]] = p.addSuit(1102).Pos
	near("金衣服 att", p.relateRatio(pi, 1, false), 1.15)

	sl.Book[0] = p.addBook(13401).Pos // 紫法术书
	near("金武器 + 紫书 hp", p.relateRatio(pi, 1, true), 1.15*1.05)
	sl.Book[1] = p.addBook(14505).Pos // 再穿金书：金书档生效
	near("金武器 + 金书 hp", p.relateRatio(pi, 1, true), 1.15*1.10)

	lao := p.addPet(petTypeByName(t, "太上老君"))
	lu.Slots[1].Pet = lao.BagPos
	near("金衣服 + 太上老君 att", p.relateRatio(pi, 1, false), 1.15*1.10)

	// 属性 = floor((等级公式 + 装备加成) × 缘分倍率)
	att, hp := p.petStats(pet)
	ga, gh := p.gearBonus(1)
	if want := int32(float64(pi.Att(pet.Grade)+ga) * 1.15 * 1.10); att != want {
		t.Fatalf("att %d, want %d", att, want)
	}
	if want := int32(float64(pi.HP(pet.Grade)+gh) * 1.15 * 1.10); hp != want {
		t.Fatalf("hp %d, want %d", hp, want)
	}
	// 不在出战阵容：没有缘分
	sl.Pet = 0
	near("下阵", p.relateRatio(pi, p.fightPos(pet.BagPos), false), 1)
}

// TestRelateReplaceName：缘分护法名可替换（relate_rep：唐僧 ↔ 旃檀功德佛）。达摩缘分 1380「唐僧同阵 att 10」。
func TestRelateReplaceName(t *testing.T) {
	p := newPlayer(1)
	lu := p.lineup(p.FightNo)
	for _, sl := range lu.Slots {
		sl.Pet = 0
	}
	dm := p.addPet(petTypeByName(t, "达摩"))
	lu.Slots[0].Pet = dm.BagPos
	pi := gamedata.Pets[dm.Type]
	if r := p.relateRatio(pi, 1, false); r != 1 {
		t.Fatalf("alone %.3f", r)
	}
	lu.Slots[1].Pet = p.addPet(petTypeByName(t, "旃檀功德佛")).BagPos
	if r := p.relateRatio(pi, 1, false); math.Abs(r-1.10) > 1e-9 {
		t.Fatalf("with 旃檀功德佛 %.3f, want 1.10", r)
	}
}
