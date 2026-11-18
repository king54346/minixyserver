package xymsrv

import (
	"testing"
	"time"
	"turnrpg/internal/battle"
	"turnrpg/internal/xym"
)

func TestIntegratedCardFrames(t *testing.T) {
	now := time.Now().Unix()
	p := &Player{CardFrames: []CardFrame{{Key: 1, Type: 1, Begin: now, Using: 13010}, {Key: 2, Type: 3, Begin: now - 9*86400}}}
	if a, h := p.frameBonus(&Pet{Type: 13010}); a != 40 || h != 0 {
		t.Fatal(a, h)
	}
	if changed := p.expireFrames(now); changed == nil || len(p.CardFrames) != 1 {
		t.Fatal("unworn expired frame deletion was not detected")
	}
	if frameAlive(CardFrame{Type: 99}, now) {
		t.Fatal("unknown frame treated as permanent")
	}
	f := &battle.Fighter{Pos: 1, FrameType: 1}
	bare := &battle.Fighter{Pos: 2} // 没边框也要占一条（发 0），覆盖客户端上一场的旧映射
	foe := &battle.Fighter{Pos: 102, FrameType: 7}
	var got string
	for _, param := range recordParams([]*battle.Fighter{f, bare}, []*battle.Fighter{foe}) {
		if param["idx"] == 14 {
			got = param["value"].(string)
		}
	}
	if got != "1:1|2:0|102:7" {
		t.Fatalf("replay frames lost: %q", got)
	}
	spec := xym.ByName("CARD_FRAME_PTO.S_OPEN_FRAME_BAG")
	if spec == nil {
		t.Fatal("missing client protocol")
	}
	spec.MustEncode(xym.Msg{"card_bag": []xym.Msg{{"frame_type": 1, "key": 1, "begin_time": now, "using_pet_type": 13010}}})
}
