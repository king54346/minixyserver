package battle

import "testing"

// 用户规则 item4：诈死触发多次的回合数差值需 >= 3（第 1 回合触发后第 4 回合方可再触发）。
// CD 判定见 effects.go：t.zhasiAt == 0 || b.round-t.zhasiAt >= 3。
func TestZhasiTriggerCooldown(t *testing.T) {
	src := newUnit(fu(101, 100, 10000, 1), 1)
	u := newUnit(fu(1, 100, 1000, 1, levelID(famZhaSi, 10)), 0)
	b := faqBattle(src, u)
	k := u.tals[famZhaSi]
	setParam(k, 0, 1)   // 血量阈值 1%
	setParam(k, 1, 100) // 必发

	// 第 1 回合触发：免死留 5 血。
	b.round = 1
	u.hp = 300
	b.hurt(src, u, 1, 500, true)
	if u.hp != 5 || u.zhasiAt != 1 {
		t.Fatalf("first trigger wrong: hp=%d zhasiAt=%d", u.hp, u.zhasiAt)
	}
	b.tickStatus(u, false) // 清掉 stZhasi，否则同回合无敌

	// 第 2、3 回合：CD 内不再触发，伤害正常致死。
	for round := 2; round <= 3; round++ {
		b.round = round
		u.hp = 300
		b.hurt(src, u, 1, 500, true)
		if u.alive() || u.zhasiAt != 1 {
			t.Fatalf("round %d must not retrigger in CD: hp=%d zhasiAt=%d", round, u.hp, u.zhasiAt)
		}
	}

	// 第 4 回合：间隔 3 回合，可以再次触发。
	b.round = 4
	u.hp = 300
	b.hurt(src, u, 1, 500, true)
	if u.hp != 5 || u.zhasiAt != 4 {
		t.Fatalf("round 4 must retrigger: hp=%d zhasiAt=%d", u.hp, u.zhasiAt)
	}
}
