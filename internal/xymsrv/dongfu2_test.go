package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// dfWorld：A 创建洞府「测试府」，B 直接加入。
func dfWorld(t *testing.T) (*Server, *client, *client, *Player, *Player) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	a, b := enterWorld(t, addr, "dfa"), enterWorld(t, addr, "dfb")
	accA, _ := srv.accounts.Login("dfa", "pw", "")
	accB, _ := srv.accounts.Login("dfb", "pw", "")
	for _, uid := range []int32{accA.UID, accB.UID} {
		srv.roles.Update(uid, func(ro *Role) bool {
			ro.Gold, ro.Grade, ro.SoulExp, ro.Cash = 100000, 50, 100000, 10000000
			return true
		})
		srv.sessionOf(uid).role = srv.roles.Get(uid)
	}
	a.sendExt("DONGFU_PTO.C_CREATE_DONGFU", xym.Msg{"name": "测试府", "slogan": "测试", "icon": 1})
	a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_INFO")
	srv.dongfu.do(func() bool {
		df, _ := srv.dongfu.of(accA.UID)
		df.Members = append(df.Members, &DFMember{UID: accB.UID, Position: dfMember})
		df.Gold, df.Yuanqi, df.Lingqi = 5000, 5000, 1000
		return true
	})
	pa, _ := srv.players.Get(accA.UID)
	pb, _ := srv.players.Get(accB.UID)
	return srv, a, b, pa, pb
}

func dfOf(srv *Server, uid int32) *Dongfu {
	var df *Dongfu
	srv.dongfu.do(func() bool { df, _ = srv.dongfu.of(uid); return false })
	return df
}

// dfOf2 是已持洞府锁时用的 of。
func dfOf2(srv *Server, uid int32) *Dongfu { df, _ := srv.dongfu.of(uid); return df }

// TestDongfuXiuwei：领称号灵气、元宝修炼 3 次（15 经验 → 1 级），建筑 0 级时不生效，建筑升上去后攻击 +1%。
func TestDongfuXiuwei(t *testing.T) {
	srv, _, b, _, pb := dfWorld(t)
	b.sendExt("DONGFU_PTO.C_GET_DONGFU_WELFARE_INFO", nil)
	w := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_WELFARE_INFO")
	if len(w["xiuwei_list"].([]xym.Msg)) != 6 || num(w, "lingqi") != 10 {
		t.Fatalf("welfare %v", w)
	}
	gold := srv.roles.Get(pb.UID).Gold
	b.sendExt("DONGFU_PTO.C_ADD_MEMBER_XIUWEI", xym.Msg{"type": "attack", "count": 3, "need_type": 1})
	l := b.until(t, "DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST")["DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST"]["list"].([]xym.Msg)
	var atk xym.Msg
	for _, x := range l {
		if x["type"] == "attack" {
			atk = x
		}
	}
	if num(atk, "lv") != 1 || num(atk, "exp") != 5 || num(atk, "curr_exp") != 15 || num(atk, "effect_lv") != 0 ||
		num(atk, "avail_count") != 57 || srv.roles.Get(pb.UID).Gold != gold-30 {
		t.Fatalf("attack %v gold %d", atk, srv.roles.Get(pb.UID).Gold)
	}
	b.collect()
	srv.dongfu.do(func() bool { dfOf2(srv, pb.UID).build("attack").Lv = 5; return true })
	b.sendExt("DONGFU_PTO.C_MEMBER_XIUWEI_LIST", nil)
	b.expectExt("DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST")
	pb.mu.Lock()
	team, _ := pb.dyTeam(nil)
	base, _ := pb.petStats(pb.petAtFightPos(1))
	pb.mu.Unlock()
	if team[0].Att != base+base/100 {
		t.Fatalf("att %d base %d", team[0].Att, base)
	}
}

// TestDongfuBuild：建设任务上交指定装备 → 建设目标 +10、贡献 +5；升级洞府、扩建屋舍；任命守卫、挑战守卫。
func TestDongfuBuild(t *testing.T) {
	srv, a, b, _, pb := dfWorld(t)
	b.sendExt("DONGFU_PTO.C_GET_DONGFU_JOB_INFO", nil)
	job := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	if num(job, "xiuwei_task_type") != 1 || num(job, "xiuwei_task_left_times") != 1 {
		t.Fatalf("job %v", job)
	}
	kind, typ := job["xiuwei_task_need_kind"].(string), num(job, "xiuwei_task_need_type")
	pb.mu.Lock()
	item := int64(0)
	if kind == "suit" {
		item = int64(pb.addSuit(typ).ItemNum)
	} else {
		item = int64(pb.addBook(int32(typ)).Pos)
	}
	pb.mu.Unlock()
	b.sendExt("DONGFU_PTO.C_DONGFU_JOB_XIUWEI_TASK", xym.Msg{"item": item, "kind": kind})
	got := b.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	if num(got["DONGFU_PTO.S_SEND_DONGFU_JOB_INFO"], "xiuwei_task_left_times") != 0 {
		t.Fatalf("job after %v", got["DONGFU_PTO.S_SEND_DONGFU_JOB_INFO"])
	}
	df := dfOf(srv, pb.UID)
	if df.build("attack").Lv != 1 || df.member(pb.UID).Contri != 5 { // 0 → 1 级正好 10 经验
		t.Fatalf("build lv %d contri %d", df.build("attack").Lv, df.member(pb.UID).Contri)
	}
	// 升级：修为总和 ≥ 20、洞府元宝 500、元气 200
	srv.dongfu.do(func() bool {
		for _, ty := range dfXiuweiTypes[:4] {
			df.build(ty).Lv = 5
		}
		return true
	})
	a.sendExt("DONGFU_PTO.C_UPGRADE_DONGFU", nil)
	info := a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_INFO")["DONGFU_PTO.S_SEND_DONGFU_INFO"]
	if num(info, "lv") != 2 || num(info, "xiuwei_lv_sum") != 21 { // 战神山 1 + 四座各 5
		t.Fatalf("upgrade %v", info)
	}
	a.sendExt("DONGFU_PTO.C_UPGRADE_DONGFU_HOUSE", nil)
	if e := a.expectExt("DONGFU_PTO.S_SEND_DONGFU_EXPAND_INFO"); num(e, "house_lv") != 2 || df.size() != 30 {
		t.Fatalf("expand %v", e)
	}
	// 守卫
	a.sendExt("DONGFU_PTO.C_GET_CHANGE_GUARD_MEMBER_LIST", xym.Msg{"guard_type": "human"})
	if l := a.expectExt("DONGFU_PTO.S_SEND_CHANGE_GUARD_MEMBER_LIST")["list"].([]xym.Msg); len(l) != 2 {
		t.Fatalf("guard list %v", l)
	}
	a.sendExt("DONGFU_PTO.C_CHANGE_GUARD", xym.Msg{"other": pb.UID, "guard_type": "human"})
	info = a.expectExt("DONGFU_PTO.S_SEND_DONGFU_INFO")
	if g := info["guard"].([]xym.Msg); len(g) != 3 || num(g[0], "usernum") != int(pb.UID) || g[0]["name"] != "dfb" {
		t.Fatalf("guards %v", info["guard"])
	}
	a.sendExt("DONGFU_PTO.C_CHALLENGE_DONGFU_GUARD", xym.Msg{"type": "human"})
	a.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
}

// TestDongfuShop：仙灵铺 4 件货；B 用灵气出价，A 加价超过后 B 的灵气退回；到期后 A 收到护法邮件。
func TestDongfuShop(t *testing.T) {
	srv, a, b, pa, pb := dfWorld(t)
	for _, p := range []*Player{pa, pb} {
		p.mu.Lock()
		p.df().Lingqi = 1000
		p.mu.Unlock()
	}
	b.sendExt("DONGFU_PTO.C_GET_DONGFU_SHOP_GOODS_LIST", nil)
	sh := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST")
	goods := sh["list"].([]xym.Msg)
	if len(goods) != dfShopBase || num(goods[0], "bid_left_time") != -1 {
		t.Fatalf("shop %v", sh)
	}
	g := goods[0]
	lq := int64(num(g, "lingqi"))
	before := pb.df().Lingqi
	b.sendExt("DONGFU_PTO.C_BID_DONGFU_GOODS", xym.Msg{"pos": 1, "type": 1, "goods_id": g["goods_id"], "lingqi": lq, "gold": 0})
	if l := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST")["list"].([]xym.Msg); num(l[0], "bid_lingqi") != int(lq) {
		t.Fatalf("bid %v", l[0])
	}
	if pb.df().Lingqi != before-int32(lq) {
		t.Fatalf("b lingqi %d", pb.df().Lingqi)
	}
	a.sendExt("DONGFU_PTO.C_BID_DONGFU_GOODS", xym.Msg{"pos": 1, "type": 1, "goods_id": g["goods_id"], "lingqi": lq * 2, "gold": 0})
	a.expectExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST")
	srv.dongfu.do(func() bool {
		dfOf2(srv, pa.UID).Shop.Goods[0].BidEnd = 1 // 到期
		return true
	})
	b.sendExt("DONGFU_PTO.C_GET_DONGFU_SHOP_GOODS_LIST", nil)
	if l := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_SHOP_GOODS_LIST")["list"].([]xym.Msg); len(l) != dfShopBase-1 {
		t.Fatalf("after settle %v", l)
	}
	if pb.df().Lingqi != before {
		t.Fatalf("b refund %d want %d", pb.df().Lingqi, before)
	}
	srv.social.do(func() bool {
		if ms := srv.social.Mails[pa.UID]; len(ms) == 0 || ms[len(ms)-1].Title != "仙灵铺竞拍" {
			t.Fatalf("no mail %v", ms)
		}
		return false
	})
}

// TestDongfuInvade：府主开启 1 级魔潮（扣洞府元宝 150、元气 100），成员打 Boss 扣共享血量。
func TestDongfuInvade(t *testing.T) {
	srv, a, b, pa, pb := dfWorld(t)
	strongPlayer(srv, pb) // 新号的初始护法挡不住 Boss（攻击 16500）
	a.sendExt("DONGFU_PTO.C_OPEN_MONSTER_INVADE_LEVEL_INFO", nil)
	if l := a.expectExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_LEVEL_INFO")["list"].([]xym.Msg); len(l) != 2 {
		t.Fatalf("levels %v", l)
	}
	a.sendExt("DONGFU_PTO.C_OPEN_MONSTER_INVADE", xym.Msg{"level": 1, "open_hour": 12})
	a.until(t, "DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	df := dfOf(srv, pa.UID)
	srv.dongfu.do(func() bool { df.Invade.Start = nowUnix() - 10; return true }) // 不等到时间点
	if df.Gold != 5000-150 || df.Yuanqi != 4900 || df.Invade.Monster != 8 {
		t.Fatalf("open: gold %d yuanqi %d %+v", df.Gold, df.Yuanqi, df.Invade)
	}
	b.sendExt("DONGFU_PTO.C_GET_DONGFU_JOB_INFO", nil)
	if j := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO"); num(j, "invade_status") != 0 || num(j, "invade_monster_left") != 8 {
		t.Fatalf("job %v", j)
	}
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_INFO", nil)
	info := b.expectExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO")
	hp := num(info, "monster_boss_hp")
	if hp <= 0 || num(info, "monster_icon") == 0 {
		t.Fatalf("invade info %v", info)
	}
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_FIGHT", xym.Msg{"type": "monster_boss"})
	got := b.until(t, "ETC_PTO.S_REWARD_MSG")
	if after := num(got["DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO"], "monster_boss_hp"); after >= hp {
		t.Fatalf("boss hp %d -> %d", hp, after)
	}
}
