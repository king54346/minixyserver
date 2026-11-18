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

// TestRelateHelper：武则天「吾皇万岁」——借来的援护李世民也算上阵；只在本场战斗里算（helperNames）。
func TestRelateHelper(t *testing.T) {
	p := newPlayer(1)
	lu := p.lineup(p.FightNo)
	for _, sl := range lu.Slots {
		sl.Pet = 0
	}
	w := p.addPet(15036)
	lu.Slots[0].Pet = w.BagPos
	pi := gamedata.Pets[15036]
	if r := p.relateRatio(pi, 1, false); r != 1 {
		t.Fatalf("alone %.3f", r)
	}
	p.helperNames = []string{"李世民"}
	if r := p.relateRatio(pi, 1, false); math.Abs(r-1.10) > 1e-9 {
		t.Fatalf("with helper 李世民 %.3f, want 1.10", r)
	}
	lu.Slots[1].Pet = p.addPet(petTypeByName(t, "女王")).BagPos
	if r := p.relateRatio(pi, 1, false); math.Abs(r-1.21) > 1e-9 {
		t.Fatalf("helper 李世民 + 女王 %.3f, want 1.21", r)
	}
}

// TestRelateHouyuanAndTongxiu：5 星李淳风（15032）自带缘分 2186「轮回有道」（npet=袁天罡 hp 10，together_npet=伏羲 together_hp 15）：
//   - 未同修时：场下 5 星袁天罡同名卡 +8 → 援护血量 +8%；再得 1 张同名卡（+9）→ 血量 +9%（+1%）；
//   - 开启同修后代替援护：按 袁天罡 + 伏羲 合计（上限 together_hp=15%），袁天罡(+9)+伏羲(+8) → 血量 +15%。
func TestRelateHouyuanAndTongxiu(t *testing.T) {
	p := newPlayer(1)
	lu := p.lineup(p.FightNo)
	for _, sl := range lu.Slots {
		sl.Pet = 0
	}
	lcf := p.addPet(15032) // 5 星李淳风
	lu.Slots[0].Pet = lcf.BagPos
	pi := gamedata.Pets[15032]
	baseHP := pi.HP(lcf.Grade)

	ytg := p.addPet(petTypeByName(t, "袁天罡"))
	ytg.TLAdd = 8
	if r := p.relateRatio(pi, 1, true); math.Abs(r-1.08) > 1e-9 {
		t.Fatalf("援护 +8: ratio %.4f, want 1.08", r)
	}
	_, hp8 := p.petStats(lcf)
	if want := int32(float64(baseHP) * 1.08); hp8 != want {
		t.Fatalf("hp8 %d, want %d", hp8, want)
	}

	// 多一张同名卡（+9）：血量加成变为 +9%，且 fightRelatePets 包含出战的李淳风
	p.gainPet(ytg.Type)
	if ytg.TLAdd != 9 {
		t.Fatalf("TLAdd %d, want 9", ytg.TLAdd)
	}
	if r := p.relateRatio(pi, 1, true); math.Abs(r-1.09) > 1e-9 {
		t.Fatalf("援护 +9: ratio %.4f, want 1.09", r)
	}
	_, hp9 := p.petStats(lcf)
	if want := int32(float64(baseHP) * 1.09); hp9 != want || hp9 <= hp8 {
		t.Fatalf("hp9 %d (hp8 %d), want %d", hp9, hp8, want)
	}
	if rel := p.fightRelatePets([]*Pet{ytg}); len(rel) != 1 || rel[0] != lcf {
		t.Fatalf("fightRelatePets %v, want [李淳风]", rel)
	}

	// 开启 2186 同修：代替原援护，按 袁天罡(+9) + 伏羲(+8) 算，上限 15%
	fx := p.addPet(petTypeByName(t, "伏羲"))
	fx.TLAdd = 8
	lcf.Together = append(lcf.Together, 2186)
	if r := p.relateRatio(pi, 1, true); math.Abs(r-1.15) > 1e-9 {
		t.Fatalf("同修 袁天罡(+9)+伏羲(+8): ratio %.4f, want 1.15", r)
	}
}
