package xymsrv

import (
	"math/rand/v2"
	"testing"

	"turnrpg/internal/battle"
)

// TestNoCardOverlap：按客户端规则推演站位——MOVE 是交换、MOVETO_EMPTY 是放进空槽。补位不能把卡放到客户端还摆着卡
// （包括被锢魂 / 吞噬标记离场的死卡）的槽上，否则两张卡叠在一起、死卡下不去；护卫 / 救援冲到队友身前后必须退回
// （ACT_DO 4 → 自己），否则护卫者停在队友那里回不了槽位。
func TestNoCardOverlap(t *testing.T) {
	_, p, srv := actWorld(t, "ovl")
	strongPlayer(srv, p)
	bad := map[string]int{}
	for seed := uint64(0); seed < 600; seed++ {
		rng := rand.New(rand.NewPCG(seed, 3))
		var team1, team2 []*battle.Fighter
		switch seed % 3 {
		case 0:
			team2 = dyEnemy(1+int(seed%5), 1+int(seed%3), int(seed%3), rng)
			p.mu.Lock()
			team1, _ = p.dyTeam(nil)
			p.mu.Unlock()
		default:
			a, b := fakeFoeCached(int32(ldFakeUIDBase+int(seed)%100)), fakeFoeCached(int32(ldFakeUIDBase+int(seed*7)%100))
			team1, team2 = battle.CloneTeam(a.Team), battle.CloneTeam(b.Team)
			for _, f := range team1 {
				f.Pos -= 100
			}
		}
		r1, r2 := battle.CloneTeam(team1), battle.CloneTeam(team2)
		res := battle.SimulateLeaders(team1, team2, [2]int32{0, 0}, rng)
		cmds, _ := battle.DecodeRecord(res.Record)
		has := map[int64]bool{}
		for _, f := range append(r1, r2...) {
			has[int64(f.Pos)] = true
		}
		guarding := map[int]bool{} // 冲出去还没退回的护卫者
		for _, c := range cmds {
			if c.ID == battle.CmdActDo && c.Magic == 4 && len(c.Targets) > 0 { // ATTACKTYPE_PROTECT
				guarding[c.Actor] = c.Targets[0][0] != int64(c.Actor)
			}
			switch c.ID {
			case battle.CmdMove:
				from, to := int64(c.Actor), c.Magic
				if !has[to] { // 移到从没有过卡的位置：前阵会报错，后阵会把卡画到界面外（用户报）
					bad["MOVE to never-had slot"]++
				}
				has[from], has[to] = has[to], has[from]
			case battle.CmdMoveToEmpty:
				from, to := int64(c.Actor), c.Magic
				if has[to] {
					bad["MOVETO_EMPTY onto a card"]++
				}
				has[from], has[to] = false, true
			}
		}
		for pos, out := range guarding {
			if out {
				bad["guard never returned"]++
				_ = pos
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%v", bad)
	}
}
