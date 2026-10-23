package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestHelper：援助列表（路人 / 好友）、看护法、借用上阵（第 9 格，双队长技）、双方得友情点、路人发加好友提示、
// 30 分钟内同一人不再出现在列表里。
func TestHelper(t *testing.T) {
	srv, a, b := twoPlayers(t)
	const ub = 20002
	b.collect()
	a.sendExt("HELPER_PTO.C_NEED_HELPER", xym.Msg{"gid": 10101, "type": 0})
	m := a.expectExt("HELPER_PTO.S_NEW_HELPER")
	hs := m["helpers"].([]xym.Msg)
	if len(m["friends"].([]xym.Msg)) != 0 || len(hs) != 1 || num(hs[0], "uid") != ub || num(hs[0], "leadershape") == 0 {
		t.Fatalf("helper list %v", m)
	}
	a.sendExt("HELPER_PTO.C_LOOK_PET", xym.Msg{"uid": ub, "type": helpTypeOther})
	if pet := a.expectExt("PET_PTO.S_SHOW_PET"); num(pet, "type") != num(hs[0], "leadershape") || num(pet, "hp") == 0 {
		t.Fatalf("show pet %v", pet)
	}

	fa, fb := srv.roles.Get(20001).FriendPoint, srv.roles.Get(ub).FriendPoint
	a.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": 10101, "type": 0, "uid": ub, "htype": helpTypeOther,
		"pos1": 1, "pos2": 2, "pos3": 3, "pos4": 4, "pos5": 5, "pos9": helperFightPos})
	got := a.untilSkipMsg(t, "HELPER_PTO.S_OPEN_ADD_FRIEND")
	team1 := got["FIGHT_PTO.S_FIGHT_RECORD"]["team1"].([]xym.Msg)
	if last := team1[len(team1)-1]; num(last, "pos") != 9 || num(last, "icon") != num(hs[0], "leadershape") {
		t.Fatalf("helper not on slot 9: %v", team1)
	}
	if add := got["HELPER_PTO.S_OPEN_ADD_FRIEND"]; num(add, "uid") != ub || num(add, "friend_point") != helperPtOther {
		t.Fatalf("add friend %v", add)
	}
	if srv.roles.Get(20001).FriendPoint != fa+helperPtOther || srv.roles.Get(ub).FriendPoint != fb+helperPtOther {
		t.Fatalf("friend point a %d->%d b %d->%d", fa, srv.roles.Get(20001).FriendPoint, fb, srv.roles.Get(ub).FriendPoint)
	}
	if u := b.untilSkipMsg(t, "ETC_PTO.S_ATTR_UPDATE")["ETC_PTO.S_ATTR_UPDATE"]; num(u["attr"].([]xym.Msg)[0], "val") != int(fb+helperPtOther) {
		t.Fatalf("owner attr %v", u)
	}

	a.collect()
	a.sendExt("HELPER_PTO.C_NEED_HELPER", xym.Msg{"gid": 10101, "type": 0})
	if m := a.expectExt("HELPER_PTO.S_NEW_HELPER"); len(m["helpers"].([]xym.Msg)) != 0 {
		t.Fatalf("cooldown not applied: %v", m)
	}
}
