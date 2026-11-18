package xymsrv

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestCardFrameFiveStarOnly：卡牌边框只能装在五星护法上（描述「适用于五星护法」，2026-11-16 补校验）。
func TestCardFrameFiveStarOnly(t *testing.T) {
	c, p, _ := actWorld(t, "frame5")
	p.CardFrames = []CardFrame{{Key: 1, Type: 1, Begin: time.Now().Unix()}}
	p.Pets = append(p.Pets, &Pet{Type: 33028}, &Pet{Type: 35043}) // 3★ / 5★ 雷震子

	c.sendExt("CARD_FRAME_PTO.C_USE_FRAME", xym.Msg{"pet_type": int64(33028), "frame_id": int64(1), "key": int64(1)})
	c.collect()
	if p.CardFrames[0].Using != 0 {
		t.Fatalf("3★ pet should be rejected, using=%d", p.CardFrames[0].Using)
	}

	c.sendExt("CARD_FRAME_PTO.C_USE_FRAME", xym.Msg{"pet_type": int64(35043), "frame_id": int64(1), "key": int64(1)})
	c.until(t, "CARD_FRAME_PTO.S_OPEN_FRAME_BAG")
	if p.CardFrames[0].Using != 35043 {
		t.Fatalf("5★ pet should be allowed, using=%d", p.CardFrames[0].Using)
	}
}

// TestFrameFragmentsNotInShop：边框碎片已从道具商城下架（2026-11-18 用户要求），
// 来源改为「取经商店 100 经书 → 5 个」和「魔潮入侵前三名 5/3/2 个」。
func TestFrameFragmentsNotInShop(t *testing.T) {
	for _, it := range itemShop {
		if it.Type >= framePartMin && it.Type <= framePartMax {
			t.Fatalf("商城里不该再有边框碎片: %d", it.Type)
		}
	}
}

// TestFramePartTypes：随机取型的取值范围只能是 10443–10449，且七种都能出（魔潮前三名用它）。
func TestFramePartTypes(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	seen := map[int]bool{}
	for range 200 {
		for _, typ := range framePartTypes(rng, 7) {
			if typ < framePartMin || typ > framePartMax {
				t.Fatalf("bad fragment type %d", typ)
			}
			seen[typ] = true
		}
	}
	if len(seen) != framePartMax-framePartMin+1 {
		t.Fatalf("only saw %v, want all 7 fragment types", seen)
	}
}

// TestQjFramePartsExchange：取经商店 100 经书 → 5 个边框碎片（2026-11-18 用户要求）。
func TestQjFramePartsExchange(t *testing.T) {
	c, p, _ := actWorld(t, "qjframe")
	p.addItem(qjJingshu, 100)
	if n := shopFramePos(); n == 0 {
		t.Fatal("取经商店里应该有边框碎片")
	}
	pos := shopFramePos()

	c.sendExt("QUJING_PTO.C_QUJING_OPEN_GOODS_LIST", nil)
	c.until(t, "QUJING_PTO.S_QUJING_OPEN_GOODS_LIST")

	c.sendExt("QUJING_PTO.C_QUJING_BUY_GOODS", xym.Msg{"pos": int64(pos), "buy_num": int64(1), "price_key": "jingshu"})
	c.until(t, "QUJING_PTO.S_QUJING_OPEN_GOODS_LIST")
	if n := p.itemCount(10443); n != 5 {
		t.Fatalf("应换到 5 个金风玉露碎片, got %d", n)
	}
	if n := p.itemCount(qjJingshu); n != 0 {
		t.Fatalf("应扣 100 经书, 剩 %d", n)
	}
}

// shopFramePos 返回取经商店里第一款边框碎片的 pos（0 = 没有）。
func shopFramePos() int {
	for _, g := range gamedata.Qujing.Shop {
		if g.Type == 3 && g.ID >= framePartMin && g.ID <= framePartMax && g.Jingshu == 100 && g.Count == 5 {
			return g.Pos
		}
	}
	return 0
}

// TestInvadeFrameParts：魔潮前三名额外奖励边框碎片 5/3/2 个，第四名和 0 贡献不给。
func TestInvadeFrameParts(t *testing.T) {
	for rank, want := range map[int]int{0: 5, 1: 3, 2: 2} {
		got := invadeFramePartItems(rank, 10, 20045)
		if got == nil {
			t.Fatalf("第 %d 名应该有边框碎片", rank+1)
		}
		total := 0
		for k, v := range got {
			total += int(v)
			var id int
			fmt.Sscanf(k, "WP%d", &id)
			if id < framePartMin || id > framePartMax {
				t.Fatalf("第 %d 名给了非法碎片 %s", rank+1, k)
			}
		}
		if total != want {
			t.Fatalf("第 %d 名应得 %d 个碎片, got %d（%v）", rank+1, want, total, got)
		}
	}
	if got := invadeFramePartItems(3, 10, 20045); got != nil {
		t.Fatalf("第 4 名不该有边框碎片: %v", got)
	}
	if got := invadeFramePartItems(0, 0, 20045); got != nil {
		t.Fatalf("0 贡献不该有边框碎片: %v", got)
	}
}

// TestCardFrameDestroyRefund：销毁边框返还一半合成碎片（50 → 25）并移除边框（2026-11-16 用户要求）。
func TestCardFrameDestroyRefund(t *testing.T) {
	c, p, _ := actWorld(t, "framedel")
	p.CardFrames = []CardFrame{{Key: 3, Type: 3, Begin: time.Now().Unix()}}
	c.sendExt("CARD_FRAME_PTO.C_DESTROY_FRAME", xym.Msg{"frame_id": int64(3), "key": int64(3)})
	c.until(t, "CARD_FRAME_PTO.S_OPEN_FRAME_BAG")
	if len(p.CardFrames) != 0 {
		t.Fatalf("frame not removed: %+v", p.CardFrames)
	}
	if n := p.itemCount(10445); n != 25 {
		t.Fatalf("refund %d fragments, want 25", n)
	}
}

// TestFrameShortMsg：碎片不足提示必须是「50 个」而不是 itoa 的 'b'（2026-11-16 用户报）。
func TestFrameShortMsg(t *testing.T) {
	if got := frameShortMsg("金风玉露"); got != "碎片不足，金风玉露需要 50 个碎片才能合成。" {
		t.Fatalf("message = %q", got)
	}
}
