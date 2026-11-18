package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestGankJobExpires：差事页的争夺事件必须在分出胜负（或超时）后消失，不能一直挂着。
func TestGankJobExpires(t *testing.T) {
	srv, _, _ := startServerSrv(t, DefaultConfig())
	now := nowUnix()
	npc := int32(gankNPCBase + 4) // 盘丝妖窟

	// 刚打完 10 秒：还应该显示结果。
	df := &Dongfu{ID: 9001, Name: "测试府"}
	df.War = &DFWar{Gank: &DFGank{Target: npc, Side: 1, Start: now - 100, End: now - 10, Winner: 2}}
	m := xym.Msg{}
	srv.gankJob(df, m)
	if st, _ := m["gank_status"].(int); st != 2 {
		t.Fatalf("刚结束的争夺应显示 gank_status=2, got %v", m["gank_status"])
	}

	// 结束超过宽限期：必须消失。
	df.War.Gank.End = now - gankResultShow - 10
	m = xym.Msg{}
	srv.gankJob(df, m)
	if _, ok := m["gank_status"]; ok {
		t.Fatalf("早就结束的争夺不应再出现在差事页: %v", m)
	}

	// 超时没分出胜负（防守方守住）：同样要过期。
	df2 := &Dongfu{ID: 9002, Name: "测试府2"}
	df2.War = &DFWar{Gank: &DFGank{Target: npc, Side: 1, Start: now - gankDuration - gankResultShow - 10}}
	m = xym.Msg{}
	srv.gankJob(df2, m)
	if _, ok := m["gank_status"]; ok {
		t.Fatalf("超时未分胜负的争夺也应过期: %v", m)
	}
}

// TestGankExpiredBoundary：过期边界（结束时刻 + 宽限期）。
func TestGankExpiredBoundary(t *testing.T) {
	now := nowUnix()
	g := &DFGank{Start: now - 100, End: now - gankResultShow + 5, Winner: 2}
	if gankExpired(g, now) {
		t.Fatal("还在宽限期内不应判过期")
	}
	g.End = now - gankResultShow - 1
	if !gankExpired(g, now) {
		t.Fatal("宽限期已过应判过期")
	}
	// End 缺省（超时未分胜负）按 Start+持续时间算。
	g2 := &DFGank{Start: now - gankDuration - gankResultShow - 1}
	if !gankExpired(g2, now) {
		t.Fatal("无 End 的超时争夺应判过期")
	}
}
