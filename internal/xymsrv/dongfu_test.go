package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestDongfu：A 没有洞府时点洞府弹入口；花 1000 元宝创建；B 在列表里看到并申请，A 批准后 B 入府；
// 成员列表 2 人；B 捐献加洞府元宝和贡献；府主有成员时不能退出，转让后可以；洞府频道只发给成员。
func TestDongfu(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	a, b := enterWorld(t, addr, "dfowner"), enterWorld(t, addr, "dfjoiner")
	accA, _ := srv.accounts.Login("dfowner", "pw", "")
	accB, _ := srv.accounts.Login("dfjoiner", "pw", "")
	for _, uid := range []int32{accA.UID, accB.UID} {
		srv.roles.Update(uid, func(ro *Role) bool { ro.Gold, ro.Grade = 5000, 30; return true })
		srv.sessionOf(uid).role = srv.roles.Get(uid)
	}

	a.sendExt("DONGFU_PTO.C_GET_DONGFU_INFO", nil)
	if m := a.expectExt("DONGFU_PTO.S_SEND_HAS_DONGFU"); num(m, "yes") != 0 {
		t.Fatalf("has dongfu %v", m)
	}
	a.sendExt("DONGFU_PTO.C_CREATE_DONGFU", xym.Msg{"name": "小雷音寺", "slogan": "大家一起建设", "icon": 3})
	got := a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_INFO")
	if info := got["DONGFU_PTO.S_SEND_DONGFU_INFO"]; info["name"] != "小雷音寺" || info["position"] != "owner" || num(info, "member_num") != 1 {
		t.Fatalf("info %v", info)
	}
	if srv.roles.Get(accA.UID).Gold != 4000 {
		t.Fatalf("gold %d", srv.roles.Get(accA.UID).Gold)
	}
	// 重名不行
	b.sendExt("DONGFU_PTO.C_CREATE_DONGFU", xym.Msg{"name": "小雷音寺", "slogan": "x", "icon": 1})
	b.expectMsg(t, "已被使用")

	b.sendExt("DONGFU_PTO.C_GET_DONGFU_LIST", xym.Msg{"page": 1})
	l := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_LIST")["list"].([]xym.Msg)
	if len(l) != 1 || num(l[0], "has_apply") != 0 {
		t.Fatalf("list %v", l)
	}
	id := l[0]["id"]
	b.sendExt("DONGFU_PTO.C_APPLY_JOIN_DONGFU", xym.Msg{"dongfu_id": id, "reason": "求收留"})
	if m := b.expectExt("DONGFU_PTO.S_REFRESH_APPLY_JOIN_DONGFU_STATUS"); num(m, "has_apply") != 1 {
		t.Fatalf("apply %v", m)
	}
	b.collect()
	a.collect() // 申请提示
	a.sendExt("DONGFU_PTO.C_GET_DONGFU_APPLY_JOIN_LIST", nil)
	if al := a.expectExt("DONGFU_PTO.S_SEND_DONGFU_APPLY_JOIN_LIST")["list"].([]xym.Msg); len(al) != 1 || al[0]["reason"] != "求收留" {
		t.Fatalf("apply list %v", al)
	}
	a.sendExt("DONGFU_PTO.C_HANDLE_APPLY_JOIN_DONGFU", xym.Msg{"other": accB.UID, "pass": 1})
	if al := a.expectExt("DONGFU_PTO.S_SEND_DONGFU_APPLY_JOIN_LIST")["list"].([]xym.Msg); len(al) != 0 {
		t.Fatalf("apply list after %v", al)
	}
	if m := b.expectExt("DONGFU_PTO.S_SEND_HAS_DONGFU"); num(m, "yes") != 1 {
		t.Fatalf("joined %v", m)
	}
	b.collect()
	a.sendExt("DONGFU_PTO.C_GET_DONGFU_MEMBER_LIST", nil)
	if ml := a.expectExt("DONGFU_PTO.S_SEND_DONGFU_MEMBER_LIST")["list"].([]xym.Msg); len(ml) != 2 {
		t.Fatalf("members %v", ml)
	}

	b.sendExt("DONGFU_PTO.C_DONGFU_JOB_DONATE", xym.Msg{"gold": 100})
	if info := b.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_INFO")["DONGFU_PTO.S_SEND_DONGFU_INFO"]; num(info, "gold") != 100 || num(info, "contribute") != 25 {
		t.Fatalf("after donate %v", info)
	}
	b.sendExt("DONGFU_PTO.C_DONGFU_JOB_DONATE", xym.Msg{"gold": 1})
	b.expectMsg(t, "今天已经捐献过了")

	// 洞府频道
	b.sendExt("CHAT_PTO.C_CHANNEL_MSG", xym.Msg{"channel": chParty, "msg": "大家好"})
	if m := a.expectExt("CHAT_PTO.S_CHANNEL_MSG"); m["msg"] != "大家好" || num(m, "channel") != chParty {
		t.Fatalf("party chat %v", m)
	}
	b.collect()

	a.sendExt("DONGFU_PTO.C_QUIT_DONGFU", nil)
	a.expectMsg(t, "转让")
	a.sendExt("DONGFU_PTO.C_GRANT_OWNER", xym.Msg{"other": accB.UID})
	a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_INFO")
	if m := b.expectExt("DONGFU_PTO.S_SEND_MEMBER_INFO"); m["position"] != "owner" {
		t.Fatalf("grant %v", m)
	}
	a.sendExt("DONGFU_PTO.C_QUIT_DONGFU", nil)
	a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_HAS_DONGFU")
	a.sendExt("DONGFU_PTO.C_GET_DONGFU_INFO", nil)
	if m := a.expectExt("DONGFU_PTO.S_SEND_HAS_DONGFU"); num(m, "yes") != 0 {
		t.Fatalf("after quit %v", m)
	}

	// 重启后数据还在
	d2 := openDongfus(srv.players.st)
	if len(d2.List) != 1 {
		t.Fatalf("persisted %d", len(d2.List))
	}
	for _, df := range d2.List {
		if len(df.Members) != 1 || df.Members[0].UID != accB.UID || df.Gold != 100 {
			t.Fatalf("persisted %+v", df)
		}
	}
}
