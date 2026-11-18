package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestPullOutStones(t *testing.T) {
	c, p, srv := actWorld(t, "puller")

	// 1. Test is_lock serialization
	pet := p.sortedPets()[0]
	pet.Locked = false
	full := p.petFull(pet)
	if _, ok := full["is_lock"].(int); !ok {
		t.Fatalf("expected petFull['is_lock'] to be int, got %T (%v)", full["is_lock"], full["is_lock"])
	}
	if full["is_lock"] != 0 {
		t.Fatalf("expected petFull['is_lock'] to be 0, got %v", full["is_lock"])
	}

	su := p.addSuit(1001)
	su.Locked = false
	suMsg := su.msg()
	if _, ok := suMsg["is_lock"].(int); !ok {
		t.Fatalf("expected su.msg()['is_lock'] to be int, got %T (%v)", suMsg["is_lock"], suMsg["is_lock"])
	}
	if suMsg["is_lock"] != 0 {
		t.Fatalf("expected su.msg()['is_lock'] to be 0, got %v", suMsg["is_lock"])
	}

	bk := p.addBook(11001)
	bk.Locked = false
	bkMsg := bk.msg()
	if _, ok := bkMsg["is_lock"].(int); !ok {
		t.Fatalf("expected bk.msg()['is_lock'] to be int, got %T (%v)", bkMsg["is_lock"], bkMsg["is_lock"])
	}
	if bkMsg["is_lock"] != 0 {
		t.Fatalf("expected bk.msg()['is_lock'] to be 0, got %v", bkMsg["is_lock"])
	}

	// 1b. petBrief 必须带 hpadd / attadd：S_PET_LIST 缺字段会被协议层按零值编码，
	// 客户端 merge_table 用零值覆盖真实数据后，培养吸纳石（10310）的过滤器会失效。
	pet.AttAdd, pet.HPAdd = 3, 2
	brief := p.petBrief(pet)
	if v, ok := brief["attadd"].(int); !ok || v != 3 {
		t.Fatalf("expected petBrief['attadd'] to be int 3, got %T (%v)", brief["attadd"], brief["attadd"])
	}
	if v, ok := brief["hpadd"].(int); !ok || v != 2 {
		t.Fatalf("expected petBrief['hpadd'] to be int 2, got %T (%v)", brief["hpadd"], brief["hpadd"])
	}

	// 2. Test 培养吸纳石 (10310 -> C_PULL_OUT_PET_POINT)
	p.addItem(itemPullPetPoint, 1)
	pet.AttAdd = 3
	pet.HPAdd = 2
	att0 := p.itemCount(pointAttItem)
	hp0 := p.itemCount(pointHPItem)

	c.sendExt("ITEM_PTO.C_PULL_OUT_PET_POINT", xym.Msg{"npcnum": int64(pet.NpcNum)})
	c.until(t, "PET_PTO.S_OPEN_POINT_PANEL")

	if pet.AttAdd != 0 || pet.HPAdd != 0 {
		t.Fatalf("expected pet points cleared, got att=%d hp=%d", pet.AttAdd, pet.HPAdd)
	}
	if wantAtt := att0 + 3*(3+1)/2; p.itemCount(pointAttItem) != wantAtt {
		t.Fatalf("expected att pills %d, got %d", wantAtt, p.itemCount(pointAttItem))
	}
	if wantHP := hp0 + 2*(2+1)/2; p.itemCount(pointHPItem) != wantHP {
		t.Fatalf("expected hp pills %d, got %d", wantHP, p.itemCount(pointHPItem))
	}
	if p.itemCount(itemPullPetPoint) != 0 {
		t.Fatalf("expected 10310 consumed")
	}

	// 3. Test 炼魂石 (10251 -> C_PULL_OUT_PET_SOUL)
	// Make sure pet is not in lineup for test
	pet2 := p.addPet(15024)
	p.addItem(itemPullPetSoul, 1)
	pet2.SoulGrade = 3
	pet2.TalGrade = [3]int{2, 1, 0}
	pet2.TLAdd = 2
	star := petStar(pet2.Type)
	var wantSoul int32
	for g := 0; g < 3; g++ {
		wantSoul += tishenSoulCost[g][star-1]
	}
	soul0 := srv.roles.Get(p.UID).SoulExp

	c.sendExt("ITEM_PTO.C_PULL_OUT_PET_SOUL", xym.Msg{"npcnum": int64(pet2.NpcNum)})
	c.until(t, "PET_PTO.S_UPDATE_PET")

	if pet2.SoulGrade != 0 || pet2.TalGrade[0] != 0 || pet2.TalGrade[1] != 0 {
		t.Fatalf("expected talents reset, got sg=%d tal=%v", pet2.SoulGrade, pet2.TalGrade)
	}
	if pet2.TLPoint != 2*shenTongPoint[star] {
		t.Fatalf("expected tlpoint=%d, got %d", 2*shenTongPoint[star], pet2.TLPoint)
	}
	if gotSoul := srv.roles.Get(p.UID).SoulExp - soul0; gotSoul != wantSoul {
		t.Fatalf("expected refunded soul %d, got %d", wantSoul, gotSoul)
	}
	if p.itemCount(itemPullPetSoul) != 0 {
		t.Fatalf("expected 10251 consumed")
	}

	// 4. Test 炼器石 (10217 -> C_PULL_OUT_SUIT_EXP)
	p.addItem(itemPullSuitExp, 1)
	su.Grade = 5
	su.Exp = 20
	st := suitStar(su.Type)
	wantSuitExp := upTable(suitUpExp).spent(st, 5) + 20

	c.sendExt("ITEM_PTO.C_PULL_OUT_SUIT_EXP", xym.Msg{"itemnum": int64(su.ItemNum)})
	c.until(t, "ITEM_PTO.S_UPDATE_SUIT")

	if su.Grade != 1 || su.Exp != 0 {
		t.Fatalf("expected suit grade 1 exp 0, got g=%d exp=%d", su.Grade, su.Exp)
	}
	var crystal *Suit
	for _, s := range p.Suits {
		if s.Type == suitMaterialCrystal {
			crystal = s
			break
		}
	}
	if crystal == nil || crystal.Exp != wantSuitExp {
		t.Fatalf("expected crystal with exp %d, got %v", wantSuitExp, crystal)
	}
	if p.itemCount(itemPullSuitExp) != 0 {
		t.Fatalf("expected 10217 consumed")
	}

	// 5. Test 炼法石 (10249 -> C_PULL_OUT_MAGIC_EXP)
	p.addItem(itemPullMagicExp, 1)
	bk.Grade = 4
	bk.Exp = 15
	mst := magicStar(bk.Type)
	wantMagicExp := upTable(magicUpExp).spent(mst, 4) + 15

	c.sendExt("ITEM_PTO.C_PULL_OUT_MAGIC_EXP", xym.Msg{"pos": int64(bk.Pos), "type": int64(bk.Type)})
	c.until(t, "ITEM_PTO.S_UPDATE_BOOK")

	if bk.Grade != 1 || bk.Exp != 0 {
		t.Fatalf("expected book grade 1 exp 0, got g=%d exp=%d", bk.Grade, bk.Exp)
	}
	var secret *Book
	for _, b := range p.Books {
		if b.Type == magicMaterialSecret {
			secret = b
			break
		}
	}
	if secret == nil || secret.Exp != wantMagicExp {
		t.Fatalf("expected secret with exp %d, got %v", wantMagicExp, secret)
	}
	if p.itemCount(itemPullMagicExp) != 0 {
		t.Fatalf("expected 10249 consumed")
	}

	// 6. Test 悟法石 (10250 -> C_PULL_OUT_TALENT_MAGIC_EXP)
	p.addItem(itemPullTalentMagic, 1)
	pet2.MagicLv = 3
	pet2.MagicExp = 10
	pi := gamedata.Pets[pet2.Type]
	tmst := magicStar(pi.Magic)
	wantTalentExp := upTable(magicUpExp).spent(tmst, 3) + 10

	c.sendExt("ITEM_PTO.C_PULL_OUT_TALENT_MAGIC_EXP", xym.Msg{"npcnum": int64(pet2.NpcNum)})
	c.until(t, "ITEM_PTO.S_UPDATE_BOOK")

	if pet2.MagicLv != 1 || pet2.MagicExp != 0 {
		t.Fatalf("expected pet talent magic grade 1 exp 0, got g=%d exp=%d", pet2.MagicLv, pet2.MagicExp)
	}
	var secret2 *Book
	for _, b := range p.Books {
		if b.Type == magicMaterialSecret && b != secret {
			secret2 = b
			break
		}
	}
	if secret2 == nil || secret2.Exp != wantTalentExp {
		t.Fatalf("expected second secret with exp %d, got %v", wantTalentExp, secret2)
	}
	if p.itemCount(itemPullTalentMagic) != 0 {
		t.Fatalf("expected 10250 consumed")
	}
}
