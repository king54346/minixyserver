package xymsrv

import (
	"strings"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func swFixNow(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	cur := at
	old := swNow
	swNow = func() time.Time { return cur }
	t.Cleanup(func() { swNow = old })
	return &cur
}

// TestStarwar：周一普通星战——报名 4 星 5V5 扣 5 万银两、记活跃；20:00 开赛后回 S_OPEN_FIGHT（第 1 轮，剩 8 人）；
// 比赛打完回 S_GAME_OFF、排行有决赛和两场半决赛、能看自己的录像、奖励按名次发邮件。
func TestStarwar(t *testing.T) {
	now := swFixNow(t, time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)) // 周一
	c, p, srv := actWorld(t, "swer")
	srv.roles.Update(p.UID, func(ro *Role) bool { ro.Cash = 100000; return true })

	c.sendExt("STARWAR_PTO.C_OPEN_STARWAR", nil)
	if m := c.expectExt("STARWAR_PTO.S_OPEN_STARWAR"); num(m, "war_type") != 0 || num(m, "ctype") != 0 ||
		num(m, "lefttime") != 8*3600 || num(m, "reward4") != int(swShowPool(4)) {
		t.Fatalf("open %v", m)
	}
	var fs []xym.Msg
	pos := swPos[5]
	for i, pet := range p.sortedPets()[:5] {
		fs = append(fs, xym.Msg{"pos": int64(pos[i]), "npcnum": int64(pet.NpcNum)})
	}
	// 3 星场：有 4 星卡（34001）不能上
	c.sendExt("STARWAR_PTO.C_SIGN_USER", xym.Msg{"ctype": 3, "ftype": 5, "fighter": fs})
	c.expectMsg(t, "3星及以下")
	c.collect()
	c.sendExt("STARWAR_PTO.C_SIGN_USER", xym.Msg{"ctype": 4, "ftype": 5, "fighter": fs})
	got := c.until(t, "STARWAR_PTO.S_OPEN_STARWAR")
	if m := got["STARWAR_PTO.S_OPEN_STARWAR"]; num(m, "ctype") != 4 || num(m, "ftype") != 5 {
		t.Fatalf("signed %v", got)
	}
	if ro := srv.roles.Get(p.UID); ro.Cash != 50000 || p.tasks().Today[evStarwar] != 1 {
		t.Fatalf("cash %d", ro.Cash)
	}
	c.sendExt("STARWAR_PTO.C_SIGN_USER", xym.Msg{"ctype": 4, "ftype": 8, "fighter": fs})
	c.expectMsg(t, "已经报名")
	c.collect()
	c.sendExt("STARWAR_PTO.C_APPLY_PET_LIST", nil)
	if l := c.expectExt("STARWAR_PTO.S_OPEN_PET_LIST")["fighter"].([]xym.Msg); len(l) != 5 || num(l[3], "pos") != 6 ||
		num(l[0], "grade") != 100 || num(l[0], "hp") <= 0 {
		t.Fatalf("pet list %v", l)
	}

	*now = time.Date(2026, 10, 19, 20, 0, 1, 0, time.Local)
	c.sendExt("STARWAR_PTO.C_OPEN_STARWAR", nil)
	f := c.expectExt("STARWAR_PTO.S_OPEN_FIGHT")
	if num(f, "ctype") != 4 || num(f, "round") != 1 || num(f, "leftfighter") != 8 || num(f, "left") != swRoundSecs-1 ||
		num(f, "reward") != int(swFee[4])*swSize[5] {
		t.Fatalf("fight %v", f)
	}
	c.sendExt("STARWAR_PTO.C_APPLY_RECORD", nil)
	if r := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD"); r["name1"] != "swer" || len(r["record"].([]byte)) == 0 {
		t.Fatalf("record %v", r)
	}
	c.sendExt("STARWAR_PTO.C_TARGET_PET_LIST", nil)
	if m := c.expectExt("STARWAR_PTO.S_TARGET_PET_LIST"); m["name"] == "" || len(m["fighter"].([]xym.Msg)) != 5 {
		t.Fatalf("target %v", m)
	}

	*now = time.Date(2026, 10, 19, 21, 0, 0, 0, time.Local)
	c.sendExt("STARWAR_PTO.C_OPEN_STARWAR", nil)
	c.expectExt("STARWAR_PTO.S_GAME_OFF")
	c.sendExt("STARWAR_PTO.C_APPLY_RANK", xym.Msg{"ctype": 4, "ftype": 5})
	rk := c.expectExt("STARWAR_PTO.S_SEND_RANK")
	if l := rk["list"].([]xym.Msg); len(l) != 3 || num(l[0], "round") != 2 || num(rk, "topreward") != int(swFee[4])*swSize[5]*40/100 {
		t.Fatalf("rank %v", rk)
	}
	var place int
	srv.starwar.do(func() bool {
		b := srv.starwar.Brackets[swKey(4, 5)]
		if !b.Paid || len(b.Rounds) != 4 {
			t.Fatalf("bracket paid %v rounds %d", b.Paid, len(b.Rounds))
		}
		place = b.place(b.indexOf(p.UID))
		return false
	})
	mails := 0
	srv.social.do(func() bool {
		for _, ml := range srv.social.Mails[p.UID] {
			if strings.Contains(ml.Title, "星战") {
				mails++
			}
		}
		return false
	})
	if want := b2i(swShare[place] > 0); mails != want {
		t.Fatalf("place %d mails %d", place, mails)
	}
}

// TestSuperStarwar：周二超级星战——8 人上满带队长位报名（不收费）；属性是 100 级统一属性、法术 6 级、
// 带一本有缘法术书（精修 3）、神通取玩家数据；管理接口开赛后回 S_OPEN_FIGHT{ctype 10}。
func TestSuperStarwar(t *testing.T) {
	swFixNow(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)) // 周二
	c, p, srv := actWorld(t, "superswer")
	p.mu.Lock()
	for _, typ := range []int32{35004, 35041, 15037} {
		p.addPet(typ)
	}
	pets := p.sortedPets()
	p.mu.Unlock()
	if len(pets) < 8 {
		t.Fatalf("pets %d", len(pets))
	}
	c.sendExt("STARWAR_PTO.C_OPEN_STARWAR", nil)
	if m := c.expectExt("STARWAR_PTO.S_OPEN_STARWAR"); num(m, "war_type") != swSuper || num(m, "ctype") != 0 {
		t.Fatalf("open %v", m)
	}
	var fs []xym.Msg
	for i, pet := range pets[:8] {
		fs = append(fs, xym.Msg{"pos": int64(i + 1), "npcnum": int64(pet.NpcNum)})
	}
	c.sendExt("STARWAR_PTO.C_SIGN_USER", xym.Msg{"ctype": 4, "ftype": 5, "fighter": fs[:5]})
	c.expectMsg(t, "超级星战")
	c.collect()
	cash := srv.roles.Get(p.UID).Cash
	c.sendExt("STARWAR_PTO.C_SUPER_SIGN_USER", xym.Msg{"ctype": swSuper, "ftype": 8, "leader_pos": 2, "fighter": fs})
	if m := c.until(t, "STARWAR_PTO.S_OPEN_STARWAR")["STARWAR_PTO.S_OPEN_STARWAR"]; num(m, "ctype") != swSuper {
		t.Fatalf("signed %v", m)
	}
	if srv.roles.Get(p.UID).Cash != cash {
		t.Fatal("super star war should be free")
	}
	srv.starwar.do(func() bool {
		e := srv.starwar.Signs[p.UID].Entry
		if e.Leader != 2 || e.Skill != gamedata.Pets[pets[1].Type].LeaderSkill || len(e.Team) != 8 {
			t.Fatalf("entry leader %d skill %d", e.Leader, e.Skill)
		}
		for i, f := range e.Team {
			pi := gamedata.Pets[pets[i].Type]
			tals, _ := p.activeTalents(pets[i], pi)
			want := superFighter(pi, pets[i].Type, i+1, tals, nil, false)
			if f.Grade != 100 || f.MagicLv != 6 || f.Att != want.Att || f.HP != want.HP || f.Att <= pi.Att(100) ||
				len(f.Talents) != len(tals) {
				t.Fatalf("fighter %d: %+v want att %d hp %d", i, f, want.Att, want.HP)
			}
			if len(f.Books) > 0 && (f.Books[0][1] != 6 || f.BookJX[0] != 3) {
				t.Fatalf("book %+v", f.Books)
			}
		}
		return false
	})
	srv.StarwarStartNow()
	c.sendExt("STARWAR_PTO.C_OPEN_STARWAR", nil)
	if f := c.expectExt("STARWAR_PTO.S_OPEN_FIGHT"); num(f, "ctype") != swSuper || num(f, "round") != 1 ||
		num(f, "leftfighter") != swSuperSize/2 {
		t.Fatalf("fight %v", f)
	}
	c.sendExt("STARWAR_PTO.C_APPLY_USER_COUNT", xym.Msg{"ctype": swSuper})
	if m := c.expectExt("STARWAR_PTO.S_SEND_USER_COUNT"); num(m, "count8") != swSuperSize {
		t.Fatalf("count %v", m)
	}
}

// TestSuperFighterStats：超级星战属性照客户端 evalPetAttackAndHP（如来佛祖：有缘武器、衣服）。
func TestSuperFighterStats(t *testing.T) {
	pi := gamedata.Pets[35004]
	f := superFighter(pi, 35004, 1, nil, nil, false)
	ra, rh := 1.0, 1.0
	var w, h float64
	for _, id := range pi.Relate {
		r := gamedata.Relates[id]
		if len(r.Att) > 0 {
			ra *= 1 + r.Att[0]/100
		}
		if len(r.HP) > 0 {
			rh *= 1 + r.HP[0]/100
		}
		if len(r.NEquip) > 0 {
			switch gamedata.Suits[int(r.NEquip[0])].Kind {
			case "ITK_WEAPON":
				w = 706
			case "ITK_SHIRT":
				h = 2824
			}
		}
	}
	wantA := int32((float64(pi.Att(100)) + w + 30) * floor2(ra))
	wantH := int32((float64(pi.HP(100)) + h + 120) * floor2(rh))
	if f.Att != wantA || f.HP != wantH || f.Speed != pi.Speed {
		t.Fatalf("got %d/%d want %d/%d", f.Att, f.HP, wantA, wantH)
	}
}
