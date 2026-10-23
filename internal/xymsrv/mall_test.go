package xymsrv

import (
	"math/rand/v2"
	"slices"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestRecruit：10 级号依次银两免费单抽、银两十连、超级请神符免费单抽、无道具十连被拒。
func TestRecruit(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, "gacha").conn.Close()
	acc, _ := srv.accounts.Login("gacha", "pw", "")
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Grade, ro.Cash = 10, 150000; return true })

	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("gacha", "pw"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()

	c.sendExt("MALL_PTO.C_OPEN_PET_MALL", nil)
	if m := c.expectExt("MALL_PTO.S_OPEN_PET_MALL"); m["freepet3count"] != int64(cashFreePerDay) || m["costpet3count"] != int64(cashPaidPerDay) {
		t.Fatalf("mall %v", m)
	}
	buy := func(typ int, wantPets int, wantShow int64) []xym.Msg {
		t.Helper()
		c.sendExt("MALL_PTO.C_BUY_PET", xym.Msg{"type": typ, "count": 1})
		var pets []xym.Msg
		for {
			main, body := c.recv()
			if main == xym.SMsg {
				t.Fatalf("buy %d refused: %s", typ, body)
			}
			_, spec, m, err := xym.DecodeExt(xym.S2C, body)
			if err != nil {
				t.Fatal(err)
			}
			switch specName(spec) {
			case "PET_PTO.S_UPDATE_PET":
				pets = m["pets"].([]xym.Msg)
			case "ETC_PTO.S_REWARD_MSG":
				if m["type"] != wantShow || len(m["list"].([]xym.Msg)) != wantPets {
					t.Fatalf("reward %v", m)
				}
			case "MALL_PTO.S_OPEN_PET_MALL":
				if len(pets) != wantPets {
					t.Fatalf("got %d pets, want %d", len(pets), wantPets)
				}
				return pets
			}
		}
	}
	buy(3, 1, 6) // 银两免费单抽
	pets := buy(13, 10, 9)
	for _, pet := range pets {
		if st := gamedata.Pets[int32(pet["type"].(int64))].Star; st > 3 {
			t.Errorf("银两池抽出 %d 星", st)
		}
	}
	for _, pet := range buy(1, 1, 16) { // 超级请神符：到点免费；元宝池用 YUANBAO_SHOWCARD（可转元神）
		if st := gamedata.Pets[int32(pet["type"].(int64))].Star; st < 3 {
			t.Errorf("超级池抽出 %d 星", st)
		}
	}
	c.sendExt("MALL_PTO.C_BUY_PET", xym.Msg{"type": 11, "count": 1}) // 没有道具
	if main, _ := c.recv(); main != xym.SMsg {
		t.Fatal("ten-pull without items should be refused")
	}

	p, _ := srv.players.Get(acc.UID)
	role := srv.roles.Get(acc.UID)
	cards := len(p.Pets) // 抽到的同名卡并进原卡（tlpoint_add 记张数）
	for _, pet := range p.Pets {
		cards += pet.TLAdd
	}
	if cards != len(starterPets)+12 || role.Cash != 150000-100000 || p.Mall.Free3Left != cashFreePerDay-1 || p.Mall.Cost3Left != cashPaidPerDay-10 {
		t.Fatalf("cards %d cash %d mall %+v", cards, role.Cash, p.Mall)
	}
}

// TestDrawRules：超级请神符十连必出 5 星、连续 10 抽内必有 5 星；各池只出池内的卡。
func TestDrawRules(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	mall := &Mall{Pity: map[int]int{}}
	for range 2000 {
		got := drawPets(recruitPools[1], mall, 10, rng)
		five := false
		for _, typ := range got {
			five = five || gamedata.Pets[typ].Star == 5
		}
		if !five {
			t.Fatalf("ten-pull without 5 star: %v", got)
		}
	}
	mall.Pity[1] = 0
	since := 0
	for range 5000 {
		typ := drawPets(recruitPools[1], mall, 1, rng)[0]
		if since++; gamedata.Pets[typ].Star == 5 {
			since = 0
		}
		if since >= 10 {
			t.Fatal("10 single draws without 5 star")
		}
	}
	for ptype, pool := range recruitPools {
		for _, typ := range drawPets(pool, &Mall{Pity: map[int]int{}}, 10, rng) {
			in := ptype == 5 && slices.ContainsFunc([]int{1, 2, 3, 4}, func(r int) bool { return slices.Contains(rarePets[r], typ) }) // 每日推荐 2% 稀有卡
			for _, name := range gamedata.Pets[typ].Pools {
				in = in || name == pool.name
			}
			if !in {
				t.Errorf("pool %d drew %d (%s) not in %s", ptype, typ, gamedata.Pets[typ].Name, pool.name)
			}
		}
	}
}

// TestSetLineup：把新抽到的卡换上阵，出战位变化的卡回 S_UPDATE_PET，阵容落盘。
func TestSetLineup(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	c := enterWorld(t, addr, "lineup")
	acc, _ := srv.accounts.Login("lineup", "pw", "")
	p, _ := srv.players.Get(acc.UID)
	p.mu.Lock()
	newPet := p.addPet(15024) // 鬼谷子
	p.mu.Unlock()

	info := []xym.Msg{}
	for pos := 1; pos <= lineupSlots; pos++ {
		pet := 0
		switch {
		case pos == 1:
			pet = newPet.BagPos // 新卡顶替 1 号位
		case pos <= len(starterPets):
			pet = pos
		}
		info = append(info, xym.Msg{"pos": pos, "pet": pet})
	}
	c.sendExt("LINEUP_PTO.C_SET_LINEUPS", xym.Msg{"fno": 1, "lineups": []xym.Msg{{"lineup_no": 1, "leader": 1, "info": info}}})
	pets := c.expectExt("PET_PTO.S_UPDATE_PET")["pets"].([]xym.Msg)
	if !hasPetUpdate(pets, newPet.NpcNum, 1) || !hasPetUpdate(pets, firstNpcNum, 0) { // 新卡上阵、原 1 号位下阵（出战卡因缘分也一并回）
		t.Fatalf("changed pets %v", pets)
	}
	c.expectExt("ITEM_PTO.S_WEAR_SUIT")
	c.expectExt("ITEM_PTO.S_WEAR_BOOK")
	if m := c.expectExt("PET_PTO.S_SET_LEADER_SUC"); m["skill"] != int64(gamedata.Pets[15024].LeaderSkill) {
		t.Fatalf("leader %v", m)
	}
	c.expectExt("LINEUP_PTO.S_SEND_LINEUP")
	if p.fightPos(newPet.BagPos) != 1 || p.fightPos(1) != 0 {
		t.Fatalf("fight pos new=%d old=%d", p.fightPos(newPet.BagPos), p.fightPos(1))
	}
}

// TestClientLocks：客户端本地等待锁表里的请求都能找到解锁回包（没实现的会走兜底）。
func TestClientLocks(t *testing.T) {
	var local map[string]string
	gamedata.MustLoad("client_locks.json", &local)
	if len(local) < 50 {
		t.Fatalf("only %d client locks", len(local))
	}
	for req := range local {
		c := xym.ByName(req)
		if c == nil {
			t.Errorf("%s: unknown request", req)
			continue
		}
		if handlers[c.PID] == nil && lockReply()[c.PID] == nil {
			t.Errorf("%s: no handler and no unlock reply", req)
		}
	}
}

// TestPetChangePos：主阵容换人（C_PET_CHANGE_POS）——新卡顶替 1 号位，回 S_UPDATE_PET。
func TestPetChangePos(t *testing.T) {
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	c := enterWorld(t, addr, "swap")
	acc, _ := srv.accounts.Login("swap", "pw", "")
	p, _ := srv.players.Get(acc.UID)
	p.mu.Lock()
	newPet := p.addPet(15024)
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_PET_CHANGE_POS", xym.Msg{"bag_pos": newPet.BagPos, "npcnum": newPet.NpcNum, "fight_pos": 1})
	got := c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
	if pets := got["PET_PTO.S_UPDATE_PET"]["pets"].([]xym.Msg); !hasPetUpdate(pets, newPet.NpcNum, 1) ||
		!hasPetUpdate(pets, firstNpcNum, 0) {
		t.Fatalf("changed %v", pets)
	}
	if got["ITEM_PTO.S_WEAR_SUIT"] == nil || got["ITEM_PTO.S_WEAR_BOOK"] == nil {
		t.Fatalf("no wear refresh: %v", got)
	}
	if p.fightPos(newPet.BagPos) != 1 || p.fightPos(1) != 0 {
		t.Fatalf("fight pos new=%d old=%d", p.fightPos(newPet.BagPos), p.fightPos(1))
	}
	// 对调：装备跟着护法走
	p.mu.Lock()
	lu := p.lineup(p.FightNo)
	lu.Slots[0].Suit = [3]int{11, 12, 13}
	p.mu.Unlock()
	c.sendExt("PET_PTO.C_PET_REPLACE", xym.Msg{"frompos": 1, "topos": 2})
	c.until(t, "LINEUP_PTO.S_SEND_LINEUP")
	if p.fightPos(newPet.BagPos) != 2 {
		t.Fatalf("after replace %d", p.fightPos(newPet.BagPos))
	}
	if lu.Slots[1].Suit != [3]int{11, 12, 13} {
		t.Fatalf("suit did not follow pet: %v / %v", lu.Slots[0], lu.Slots[1])
	}
	// 「卸下当前护法」：发该卡自己所在的位置
	c.sendExt("PET_PTO.C_PET_CHANGE_POS", xym.Msg{"bag_pos": newPet.BagPos, "npcnum": newPet.NpcNum, "fight_pos": 2})
	if pets := c.until(t, "LINEUP_PTO.S_SEND_LINEUP")["PET_PTO.S_UPDATE_PET"]["pets"].([]xym.Msg); !hasPetUpdate(pets, newPet.NpcNum, 0) || p.fightPos(newPet.BagPos) != 0 {
		t.Fatalf("unload %v", pets)
	}
}

// hasPetUpdate：S_UPDATE_PET 里有卡 npc，且出战位是 fp。
func hasPetUpdate(pets []xym.Msg, npc int32, fp int) bool {
	for _, p := range pets {
		if p["npcnum"] == int64(npc) && p["fight_pos"] == int64(fp) {
			return true
		}
	}
	return false
}
