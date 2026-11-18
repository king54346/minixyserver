package xymsrv

import (
	"testing"
	"time"

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

// TestDongfuXiuwei：领称号灵气、元宝修炼 3 次（15 经验 → 1 级），建筑 0 级时不生效；
// 建筑升上去后面板攻击 / 生命 +1%（petStats 下发即面板），出战值与面板一致，
// 气血修为装上 DaoImmune（几率免疫道属性伤害）、人族修为装上属性减伤。
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
	pb.mu.Lock()
	att0, hp0 := pb.petStats(pb.petAtFightPos(1))
	pb.mu.Unlock()
	pb.df().Exp["hp"] = 10    // 气血修为 1 级
	pb.df().Exp["human"] = 10 // 人族修为 1 级
	srv.dongfu.do(func() bool {
		d := dfOf2(srv, pb.UID)
		d.build("attack").Lv = 5
		d.build("hp").Lv = 5
		d.build("human").Lv = 5
		return true
	})
	b.sendExt("DONGFU_PTO.C_MEMBER_XIUWEI_LIST", nil) // 刷新 Caps
	b.expectExt("DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST")
	panel := b.expectExt("PET_PTO.S_UPDATE_PET")
	pb.mu.Lock()
	team, _ := pb.dyTeam(nil)
	base, hp := pb.petStats(pb.petAtFightPos(1))
	pb.mu.Unlock()
	if base != att0+att0/100 || hp != hp0+hp0/100 {
		t.Fatalf("面板 %d/%d 应为 %d+%d/100 / %d+%d/100", base, hp, att0, att0/100, hp0, hp0/100)
	}
	if team[0].Att != base || team[0].HP != hp || team[0].MaxHP != hp {
		t.Fatalf("出战 %d %d/%d 应与面板一致 %d %d", team[0].Att, team[0].HP, team[0].MaxHP, base, hp)
	}
	var panelPet xym.Msg
	for _, row := range panel["pets"].([]xym.Msg) {
		if num(row, "npcnum") == int(team[0].ID) {
			panelPet = row
			break
		}
	}
	if panelPet == nil || num(panelPet, "att") != int(base) || num(panelPet, "hp") != int(hp) {
		t.Fatalf("下发面板 %v 与出战属性 %d/%d 不一致", panelPet, base, hp)
	}
	if team[0].DaoImmune != 1 {
		t.Fatalf("DaoImmune %d 应为气血修为 1 级 = 1（1%% 几率免疫道属性伤害）", team[0].DaoImmune)
	}
	if team[0].ElemRes[1] != 1 {
		t.Fatalf("ElemRes[土] %d 应为人族修为 1 级 = 1", team[0].ElemRes[1])
	}

	// 验证 30 级角色修为达到 15 级时，exp_next 不为 0（不显示满级），且可以继续修炼
	srv.roles.Update(pb.UID, func(r *Role) bool { r.Grade = 30; return true })
	pb.df().Exp["attack"] = 1200 // 刚好升到 15 级
	b.sendExt("DONGFU_PTO.C_MEMBER_XIUWEI_LIST", nil)
	l2 := b.expectExt("DONGFU_PTO.S_SEND_MEMBER_XIUWEI_LIST")["list"].([]xym.Msg)
	for _, x := range l2 {
		if x["type"] == "attack" {
			if num(x, "lv") != 15 || num(x, "exp_next") == 0 {
				t.Fatalf("lv 15 should not be maxed, got lv=%v exp_next=%v", x["lv"], x["exp_next"])
			}
		}
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
	// 升级：修为总和 ≥ 20、洞府元宝 500、元气 200；触发魔潮入侵，失败则升级失败，成功则升到 2 级
	srv.dongfu.do(func() bool {
		for _, ty := range dfXiuweiTypes[:4] {
			df.build(ty).Lv = 5
		}
		return true
	})
	a.sendExt("DONGFU_PTO.C_UPGRADE_DONGFU", nil)
	info := a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_INFO")["DONGFU_PTO.S_SEND_DONGFU_INFO"]
	a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	if num(info, "lv") != 1 || df.Invade == nil || !df.Invade.Upgrade {
		t.Fatalf("upgrade should trigger invade first: %v %+v", info, df.Invade)
	}
	// 模拟魔潮超时失败 → 洞府仍为 1 级（升级失败）
	srv.dongfu.do(func() bool {
		df.Invade.Start = nowUnix() - invadeDuration - 10
		return true
	})
	a.sendExt("DONGFU_PTO.C_GET_DONGFU_JOB_INFO", nil)
	if j := a.expectExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO"); num(j, "invade_status") != 2 || df.Lv != 1 {
		t.Fatalf("upgrade fail expected status 2 and lv 1, got %v lv %d", j, df.Lv)
	}
	// 再次发起升级并击退魔潮 → 洞府升到 2 级
	a.sendExt("DONGFU_PTO.C_UPGRADE_DONGFU", nil)
	a.untilSkipMsg(t, "DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	strongPlayer(srv, pb)
	srv.dongfu.do(func() bool {
		df.Invade.Gov, df.Invade.Leader, df.Invade.Monster = 0, 0, 0
		df.Invade.BossStage = 3
		df.Invade.BossHP = 1
		return true
	})
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_FIGHT", xym.Msg{"type": "monster_boss"})
	b.until(t, "ETC_PTO.S_REWARD_MSG")
	if df.Lv != 2 {
		t.Fatalf("upgrade after invade win want lv 2, got %d", df.Lv)
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

// TestDongfuInvade：
//   - 1 级洞府开魔潮扣 300 洞府元宝（2 级 600、3 级 900），每天限开一次；
//   - 野生山妖 / 山妖头目 / 山妖督军各 50 名（共 150 名守卫），战斗内每击杀 1 个山妖减少 1 个剩余数；
//   - 30 级及以下打人界 +60%、40 级及以下打地界 +30%、天界无加成；
//   - 未杀完 150 名守卫前不可挑战山妖大王；山妖大王分人界 / 地界 / 天界 3 个阶段，车轮战记录血量；
//   - 每名成员限打 10 场，阵亡护法保留，前 10 个免费复活、之后每个 2 元宝。
func TestDongfuInvade(t *testing.T) {
	if invadeBuffPct(30, 1) != 60 || invadeBuffPct(31, 1) != 0 ||
		invadeBuffPct(40, 2) != 30 || invadeBuffPct(41, 2) != 0 ||
		invadeBuffPct(20, 3) != 0 {
		t.Fatalf("unexpected invadeBuffPct")
	}
	// 把「现在」固定到当天 10:00：12:02 这个时段一定在未来，开魔潮应该是「排到点」而不是立刻开。
	oldNow := invadeNow
	invadeNow = func() time.Time {
		n := time.Now()
		return time.Date(n.Year(), n.Month(), n.Day(), 10, 0, 0, 0, n.Location())
	}
	t.Cleanup(func() { invadeNow = oldNow })

	srv, a, b, pa, pb := dfWorld(t)
	strongPlayer(srv, pb)
	b.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	if events := b.expectExt("TREE_PTO.S_EVENT_LIST"); num(events, "monster_invade_left_time") != 0 {
		t.Fatal("魔潮开启前不应显示事件")
	}
	a.sendExt("DONGFU_PTO.C_OPEN_MONSTER_INVADE_LEVEL_INFO", nil)
	// 1 级洞府开 4 重（4×洞府等级，最高 20 重）。
	if l := a.expectExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_LEVEL_INFO")["list"].([]xym.Msg); len(l) != 4 {
		t.Fatalf("levels %v", l)
	}
	a.sendExt("DONGFU_PTO.C_OPEN_MONSTER_INVADE", xym.Msg{"level": 1, "open_hour": 12})
	a.until(t, "DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	b.expectExt("ETC_PTO.S_SEND_NO_CACHE_MSG")
	df := dfOf(srv, pa.UID)
	if df.Gold != 5000-300 || df.Invade.Monster != 50 || df.Invade.Leader != 50 || df.Invade.Gov != 50 {
		t.Fatalf("open: gold %d %+v", df.Gold, df.Invade)
	}
	if want := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 12, invadeMinute, 0, 0, time.Local).Unix(); df.Invade.Start != want {
		t.Fatalf("应该排到 12:%02d 才开（不是点了就开），got start=%d want=%d", invadeMinute, df.Invade.Start, want)
	}
	// 排期阶段也要出现在主界面「事件」里（倒计时 = 距开始）。
	// 真实时钟已经晚于 12:02，这里把开始时间改成「一小时后」，模拟「已排期、还没到点」。
	a.collect() // 丢掉排期时那条（当时 Start 还是 12:02，按真实时钟已经算结束）
	srv.dongfu.do(func() bool {
		df.Invade.Start = time.Now().Unix() + 3600
		return true
	})
	a.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	if ev := a.expectExt("TREE_PTO.S_EVENT_LIST"); num(ev, "monster_invade_left_time") <= 3600-60 || num(ev, "monster_invade_left_time") > 3600 {
		t.Fatalf("已排期的魔潮应显示「距开始」的倒计时，got %v", ev["monster_invade_left_time"])
	}
	// 快到点了：把开始时间拨到「刚才」，继续后面的战斗流程。
	srv.dongfu.do(func() bool {
		df.Invade.Start = time.Now().Unix() - 1
		return true
	})
	b.sendExt("DONGFU_PTO.C_GET_DONGFU_JOB_INFO", nil)
	if j := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO"); num(j, "invade_status") != 0 || num(j, "invade_monster_left") != 50 {
		t.Fatalf("job %v", j)
	}
	b.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	if events := b.expectExt("TREE_PTO.S_EVENT_LIST"); num(events, "monster_invade_left_time") <= 0 {
		t.Fatal("进行中的魔潮应在洞府成员的事件列表显示倒计时")
	}
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_INFO", nil)
	info := b.expectExt("DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO")
	hp := num(info, "monster_boss_hp")
	if hp <= 0 || num(info, "monster_icon") == 0 || num(info, "free_relive_times") != 10 || num(info, "challenge_times") != 10 {
		t.Fatalf("invade info %v", info)
	}
	// 守卫未清完前不能打山妖大王
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_FIGHT", xym.Msg{"type": "monster_boss"})
	b.untilSkipMsg(t, "DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO")
	if df.Invade.user(pb.UID).Times != 0 {
		t.Fatalf("boss fight should be blocked while guards remain")
	}
	// 打一场人界门（野生山妖），整队 5 只全灭 → 剩余从 50 减为 45
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_FIGHT", xym.Msg{"type": "monster"})
	got := b.until(t, "ETC_PTO.S_REWARD_MSG")
	if left := num(got["DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO"], "monster_left"); left != 45 {
		t.Fatalf("monster_left want 45, got %d", left)
	}
	// 清空三门守卫后挑战山妖大王：验证人界(24301) → 地界(24302) → 天界(24303) 三阶段推进
	srv.dongfu.do(func() bool {
		df.Invade.Gov, df.Invade.Leader, df.Invade.Monster = 0, 0, 0
		df.Invade.BossHP = 1
		return true
	})
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_FIGHT", xym.Msg{"type": "monster_boss"})
	got = b.until(t, "ETC_PTO.S_REWARD_MSG")
	if ca := num(got["DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO"], "monster_boss_ca"); ca != 24302 || df.Invade.BossStage != 2 {
		t.Fatalf("stage 1 -> 2 expected ca 24302 stage 2, got ca %d stage %d", ca, df.Invade.BossStage)
	}
	srv.dongfu.do(func() bool { df.Invade.BossHP = 1; return true })
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_FIGHT", xym.Msg{"type": "monster_boss"})
	got = b.until(t, "ETC_PTO.S_REWARD_MSG")
	if ca := num(got["DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO"], "monster_boss_ca"); ca != 24303 || df.Invade.BossStage != 3 {
		t.Fatalf("stage 2 -> 3 expected ca 24303 stage 3, got ca %d stage %d", ca, df.Invade.BossStage)
	}
	// 验证复活：前 10 个免费，之后每个 2 元宝
	goldBefore := srv.roles.Get(pb.UID).Gold
	srv.dongfu.do(func() bool {
		u := df.Invade.user(pb.UID)
		u.Relive = 9
		u.Dead = []int{1, 2, 3} // 1 个免费 + 2 个付费（4 元宝）
		return true
	})
	b.sendExt("DONGFU_PTO.C_MONSTER_INVADE_RELIVE_ALL_FIGHTER", nil)
	rel := b.until(t, "DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO")["DONGFU_PTO.S_SEND_MONSTER_INVADE_INFO"]
	if num(rel, "free_relive_times") != 0 || srv.roles.Get(pb.UID).Gold != goldBefore-4 {
		t.Fatalf("relive cost want -4 gold, got %d -> %d, info %v", goldBefore, srv.roles.Get(pb.UID).Gold, rel)
	}
}

func TestDongfuPushPreferences(t *testing.T) {
	// 同样把「现在」固定到当天 10:00，12:02 才是未来时段。
	oldNow := invadeNow
	invadeNow = func() time.Time {
		n := time.Now()
		return time.Date(n.Year(), n.Month(), n.Day(), 10, 0, 0, 0, n.Location())
	}
	t.Cleanup(func() { invadeNow = oldNow })

	srv, a, b, _, pb := dfWorld(t)
	settings := xym.Msg{}
	for _, field := range dfPushFields {
		settings[field] = 1
	}
	settings[dfPushFields[dfPushInvade]] = 0
	b.sendExt("DONGFU_PTO.C_SELECT_DONGFU_PUSH_MSG", settings)
	job := b.expectExt("DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	for i, field := range dfPushFields {
		if num(job, field) != settings[field].(int) {
			t.Fatalf("push setting %d did not round trip: %v", i, job[field])
		}
	}
	// The response is queued inside the guild transaction, before the write.
	// Wait for the transaction lock before verifying persisted state.
	var loaded *Dongfus
	srv.dongfu.do(func() bool {
		loaded = openDongfus(srv.players.st)
		return false
	})
	if _, member := loaded.of(pb.UID); member == nil || member.PushMuted != 1<<dfPushInvade {
		t.Fatalf("push preference did not persist: %+v", member)
	}
	a.sendExt("DONGFU_PTO.C_OPEN_MONSTER_INVADE", xym.Msg{"level": 1, "open_hour": 12})
	a.expectExt("ETC_PTO.S_SEND_NO_CACHE_MSG")
	a.until(t, "DONGFU_PTO.S_SEND_DONGFU_JOB_INFO")
	// 排到 12:02 了，这里把开始时间拨到刚才，让它变成「进行中」。
	srv.dongfu.do(func() bool {
		for _, df := range srv.dongfu.List {
			if df.Invade != nil {
				df.Invade.Start = time.Now().Unix() - 1
			}
		}
		return true
	})
	b.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	if event := b.expectExt("TREE_PTO.S_EVENT_LIST"); num(event, "monster_invade_left_time") <= 0 {
		t.Fatal("muting the push should still allow viewing the active event")
	}
}
