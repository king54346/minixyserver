package xymsrv

import (
	"strings"
	"testing"

	"turnrpg/internal/xym"
)

func TestFriendLimitMatchesClientAndIdentifiesFullSide(t *testing.T) {
	me := &Role{Grade: 35, VIP: 15}
	other := &Role{Grade: 40, VIP: 15}
	if friendLimit(me) != 75 || friendLimit(other) != 80 {
		t.Fatal("grade/VIP limit differs from client")
	}
	if msg := friendFullMessage(me, other, 6, 50); msg != "" {
		t.Fatal("six friends blocked by obsolete limit:", msg)
	}
	if msg := friendFullMessage(me, other, 6, 80); !strings.Contains(msg, "对方") || !strings.Contains(msg, "80/80") {
		t.Fatal(msg)
	}
	if msg := friendFullMessage(me, other, 75, 6); !strings.Contains(msg, "您的") || !strings.Contains(msg, "75/75") {
		t.Fatal(msg)
	}
}

func TestFriendApplyAndAcceptUseTargetsActualLimit(t *testing.T) {
	srv, a, b := twoPlayers(t)
	const me, other = 20001, 20002
	srv.roles.Update(other, func(ro *Role) bool { ro.Grade = 35; return true })
	filled := func(n int) []int32 {
		v := make([]int32, n)
		for i := range v {
			v[i] = int32(30000 + i)
		}
		return v
	}
	srv.social.do(func() bool { srv.social.Friends[me] = filled(6); srv.social.Friends[other] = filled(50); return false })
	a.sendExt("FRIEND_PTO.C_APPLY_FRIEND", xym.Msg{"fid": other, "force": 1, "need_confirm": 1})
	if msg := a.expectExt("ETC_PTO.S_RET_MSG")["msg"].(string); !strings.Contains(msg, "已发送") {
		t.Fatal(msg)
	}
	b.collect()
	// Now the target really is full; both apply and accept explain which side.
	srv.social.do(func() bool {
		srv.social.Friends[other] = filled(75)
		srv.social.Applies[me] = []int32{other}
		return false
	})
	for _, op := range []string{"FRIEND_PTO.C_APPLY_FRIEND", "FRIEND_PTO.C_ACCEPT_FRIEND"} {
		a.sendExt(op, xym.Msg{"fid": other, "force": 1, "need_confirm": 1})
		if msg := a.expectExt("ETC_PTO.S_RET_MSG")["msg"].(string); !strings.Contains(msg, "对方") || !strings.Contains(msg, "75/75") {
			t.Fatal(op, msg)
		}
		a.collect()
		b.collect()
	}
}
