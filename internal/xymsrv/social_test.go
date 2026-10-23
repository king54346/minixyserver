package xymsrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"turnrpg/internal/xym"
)

// twoPlayers 起服并让 a、b 两个账号进游戏（uid 20001、20002）。
func twoPlayers(t *testing.T) (*Server, *client, *client) {
	t.Helper()
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	a := enterWorld(t, addr, "alice")
	b := enterWorld(t, addr, "bob")
	a.collect() // b 登录时 a 可能收到推送
	return srv, a, b
}

// TestFriends：申请 → 对方收到申请列表 → 同意 → 双方好友列表；私信（对方收到有私信 → 拉取）；送礼收礼得友情点；删除。
func TestFriends(t *testing.T) {
	srv, a, b := twoPlayers(t)
	const ua, ub = 20001, 20002
	a.sendExt("FRIEND_PTO.C_APPLY_FRIEND", xym.Msg{"fid": ub, "force": 1, "need_confirm": 1})
	if m := a.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "已发送") {
		t.Fatalf("apply %v", m)
	}
	if l := b.expectExt("FRIEND_PTO.S_APPLY_FRIEND_EXT")["friends"].([]xym.Msg); len(l) != 1 || num(l[0], "uid") != ua {
		t.Fatalf("b applies %v", l)
	}
	b.sendExt("FRIEND_PTO.C_ACCEPT_FRIEND", xym.Msg{"fid": ua})
	if l := b.expectExt("FRIEND_PTO.S_FRIEND_INFO")["friends"].([]xym.Msg); len(l) != 1 || l[0]["name"] != "alice" {
		t.Fatalf("b friend %v", l)
	}
	b.expectExt("FRIEND_PTO.S_APPLY_FRIEND_EXT")
	if l := a.expectExt("FRIEND_PTO.S_FRIEND_INFO")["friends"].([]xym.Msg); len(l) != 1 || num(l[0], "is_online") != 1 {
		t.Fatalf("a friend %v", l)
	}

	a.sendExt("FRIEND_PTO.C_FRIEND_MSG", xym.Msg{"fid": ub, "msg": "你好"})
	st := b.expectExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT")["friends"].([]xym.Msg)[0]
	if num(st, "uid") != ua || num(st, "stat")&statHasMsg == 0 {
		t.Fatalf("msg stat %v", st)
	}
	b.sendExt("FRIEND_PTO.C_GET_FRIEND_MSG", xym.Msg{"fid": ua})
	if m := b.expectExt("FRIEND_PTO.S_FRIEND_MSG"); num(m, "fid") != ua || m["msg"].([]xym.Msg)[0]["text"] != "你好" {
		t.Fatalf("msg %v", m)
	}

	a.sendExt("FRIEND_PTO.C_FRIEND_OP", xym.Msg{"op": friendOpSend, "fid": ub})
	if g := num(b.expectExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT")["friends"].([]xym.Msg)[0], "gift"); g&giftCanRecv == 0 {
		t.Fatalf("b gift bits %d", g)
	}
	if n := num(b.expectExt("FRIEND_PTO.S_COUNT_GIFT"), "count"); n != 1 {
		t.Fatalf("gift count %d", n)
	}
	a.expectExt("FRIEND_PTO.S_UPDATE_FRIEND_STAT")
	fp := srv.roles.Get(ub).FriendPoint
	b.sendExt("FRIEND_PTO.C_FRIEND_OP", xym.Msg{"op": friendOpAllR, "fid": 0})
	b.expectExt("ETC_PTO.S_ATTR_UPDATE")
	if srv.roles.Get(ub).FriendPoint != fp+giftFriendPt {
		t.Fatalf("friend point %d -> %d", fp, srv.roles.Get(ub).FriendPoint)
	}
	b.collect()
	a.sendExt("FRIEND_PTO.C_FRIEND_OP", xym.Msg{"op": friendOpSend, "fid": ub}) // 今天送过了
	if m := a.expectExt("ETC_PTO.S_RET_MSG"); !strings.Contains(m["msg"].(string), "送过") {
		t.Fatalf("resend %v", m)
	}

	a.sendExt("FRIEND_PTO.C_FRIEND_OP", xym.Msg{"op": friendOpDel, "fid": ub})
	if num(a.expectExt("FRIEND_PTO.S_DEL_FRIEND"), "fid") != ub || num(b.expectExt("FRIEND_PTO.S_DEL_FRIEND"), "fid") != ua {
		t.Fatal("delete not mirrored")
	}
	a.sendExt("FRIEND_PTO.C_QUERY_ALL_FRIENDS", nil)
	if l := a.expectExt("FRIEND_PTO.S_FRIEND_INFO")["friends"].([]xym.Msg); len(l) != 0 {
		t.Fatalf("after delete %v", l)
	}
}

// TestChat：世界频道广播给在线的人（含自己），拉历史拿到刚发的。
func TestChat(t *testing.T) {
	srv, a, b := twoPlayers(t)
	srv.roles.Update(20001, func(ro *Role) bool { ro.Grade = 20; return true })
	a.sendExt("CHAT_PTO.C_CHANNEL_MSG", xym.Msg{"channel": chWorld, "msg": "大家好"})
	for _, c := range []*client{a, b} {
		if m := c.expectExt("CHAT_PTO.S_CHANNEL_MSG"); m["msg"] != "大家好" || m["name"] != "alice" || num(m, "channel") != chWorld {
			t.Fatalf("broadcast %v", m)
		}
	}
	b.sendExt("CHAT_PTO.C_FETCH_MSG", xym.Msg{"channel": chWorld, "is_latest": 1, "latest_sn": 0, "num_of_msg": 20})
	if l := b.expectExt("CHAT_PTO.S_CHANNEL_MSG_IN_RANGE")["msgs"].([]xym.Msg); len(l) == 0 || l[len(l)-1]["msg"] != "大家好" {
		t.Fatalf("history %v", l)
	}
	b.sendExt("CHAT_PTO.C_CHANNEL_MSG", xym.Msg{"channel": chWorld, "msg": "一"}) // b 是 1 级：发后 60 秒冷却
	b.expectExt("CHAT_PTO.S_CHANNEL_CD")
	b.expectExt("CHAT_PTO.S_CHANNEL_MSG")
	b.sendExt("CHAT_PTO.C_CHANNEL_MSG", xym.Msg{"channel": chWorld, "msg": "二"})
	b.expectExt("CHAT_PTO.S_CHANNEL_CD")
	if got := b.collect(); len(got) != 0 {
		t.Fatalf("message sent during cooldown: %v", got)
	}
}

// TestMail：管理接口发信（元宝 + 银两 + 道具），玩家收到未读数、看列表、全部领取、清空。
func TestMail(t *testing.T) {
	srv, a, _ := twoPlayers(t)
	h := srv.AdminHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mail",
		strings.NewReader(`{"to":20001,"title":"补偿","text":"维护补偿","gold":100,"cash":5000,"items":{"WP10317":2}}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sent":1`) {
		t.Fatalf("admin %d %s", rec.Code, rec.Body)
	}
	if m := a.expectExt("MAIL_PTO.S_GET_NEW_MAIL_NUM"); num(m, "unreward") != 1 || num(m, "unread") != 1 {
		t.Fatalf("num %v", m)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mail", strings.NewReader(`{"to":20001,"title":"x","items":{"WP1":1}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad item accepted: %d", rec.Code)
	}
	rec = httptest.NewRecorder() // GBK 编码的标题（Windows 命令行直接写中文）
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mail", strings.NewReader("{\"to\":20001,\"title\":\"\xb2\xe2\xca\xd4\"}")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GBK title accepted: %d %s", rec.Code, rec.Body)
	}

	a.sendExt("MAIL_PTO.C_OPEN_NEW_MAIL", xym.Msg{"idx": 0})
	l := a.expectExt("MAIL_PTO.S_SEND_OPEN_NEW_MAIL")["list"].([]xym.Msg)
	if len(l) != 1 || l[0]["title"] != "补偿" || len(l[0]["rewardlist"].([]xym.Msg)) != 3 {
		t.Fatalf("list %v", l)
	}
	before := srv.roles.Get(20001)
	a.sendExt("MAIL_PTO.C_CLICK_NEW_MAIL_REWARD", xym.Msg{"idx": 0})
	got := a.until(t, "MAIL_PTO.S_CLICK_NEW_MAIL_REWARD_SUCC")
	after := srv.roles.Get(20001)
	if after.Gold != before.Gold+100 || after.Cash != before.Cash+5000 || got["ETC_PTO.S_REWARD_MSG"] == nil {
		t.Fatalf("claim gold %d→%d cash %d→%d", before.Gold, after.Gold, before.Cash, after.Cash)
	}
	p, _ := srv.players.Get(20001)
	if p.itemCount(10317) != 2 {
		t.Fatalf("items %d", p.itemCount(10317))
	}
	a.sendExt("MAIL_PTO.C_CLICK_NEW_MAIL_REWARD", xym.Msg{"idx": 0}) // 领过了
	a.expectExt("ETC_PTO.S_RET_MSG")
	a.sendExt("MAIL_PTO.C_DEL_ALL_NEW_MAIL", xym.Msg{"list": []xym.Msg{}})
	if l := a.expectExt("MAIL_PTO.S_DEL_NEW_MAIL_SUCC")["list"].([]xym.Msg); len(l) != 1 {
		t.Fatalf("delete %v", l)
	}

	// 全服发：两个角色各一封
	if n, err := srv.SendMail(0, MailSpec{Title: "全服", Gold: 1}); err != nil || n != 2 {
		t.Fatalf("broadcast mail %d %v", n, err)
	}
}
