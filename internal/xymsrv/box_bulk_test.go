package xymsrv

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"turnrpg/internal/xym"
)

func TestBulkBoxesRewardsStayBounded(t *testing.T) {
	c, p, srv := actWorld(t, "bulkboxes")
	const boxes = 10000
	p.mu.Lock()
	p.addItem(10002, boxes)
	p.addItem(10003, boxes)
	var pos int
	for _, it := range p.Items {
		if it.Type == 10002 {
			pos = it.Pos
			break
		}
	}
	oldGear := len(p.Suits) + len(p.Books)
	p.mu.Unlock()
	soul := srv.roles.Get(p.UID).SoulExp
	started := time.Now()
	c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": pos, "type": 10002, "use_num": boxes, "choose": 0, "usetype": 0})
	var reward xym.Msg
	frames, maxBody := 0, 0
	for reward == nil {
		main, body := c.recv()
		frames++
		maxBody = max(maxBody, len(body))
		if main == xym.SMsg {
			t.Fatalf("bulk opening refused: %s", body)
		}
		if len(body) > maxFrameBody {
			t.Fatalf("oversized update frame: %d", len(body))
		}
		_, spec, m, err := xym.DecodeExt(xym.S2C, body)
		if err != nil {
			t.Fatal(err)
		}
		if specName(spec) == "ETC_PTO.S_REWARD_MSG" {
			reward = m
		}
	}
	if num(reward, "type") != boxNormal {
		t.Fatal("bulk opening used the ten-item animation")
	}
	var counted, droppedGear int64
	for _, g := range reward["list"].([]xym.Msg) {
		n := int64(num(g, "count"))
		typ := num(g, "type")
		if typ == 10002 && num(g, "rtype") == rtItem {
			continue
		}
		if typ == 10092 && num(g, "rtype") == rtItem {
			counted += n / 5
		} else {
			counted += n
		}
		if num(g, "rtype") == rtEquip || num(g, "rtype") == rtMagic {
			droppedGear += n
		}
	}
	if counted != boxes || len(reward["list"].([]xym.Msg)) >= boxes {
		t.Fatalf("aggregated rewards lost count: %d", counted)
	}
	p.mu.Lock()
	if p.itemCount(10003) != 0 || p.itemCount(10002) >= boxes {
		t.Fatal("chests/keys were not consumed")
	}
	ownedGear := int64(len(p.Suits) + len(p.Books) - oldGear)
	p.mu.Unlock()
	srv.social.mu.Lock()
	var mailedGear int64
	for _, m := range srv.social.Mails[p.UID] {
		if len(m.Rewards) > maxOverflowRewards {
			t.Fatal("overflow attachment list too large")
		}
		for _, r := range m.Rewards {
			if strings.HasPrefix(r.S, "ZB") || strings.HasPrefix(r.S, "FS") {
				mailedGear += int64(r.R)
			}
		}
	}
	srv.social.mu.Unlock()
	if ownedGear+mailedGear != droppedGear {
		t.Fatalf("gear lost: owned %d mail %d reward %d", ownedGear, mailedGear, droppedGear)
	}
	if got := srv.roles.Get(p.UID).SoulExp - soul; got < 20*boxes || got > 23*boxes {
		t.Fatalf("incorrect soul payout %d", got)
	}
	t.Logf("%d boxes: %d frames, max body %d bytes, %d reward types, elapsed %s", boxes, frames, maxBody, len(reward["list"].([]xym.Msg)), time.Since(started))
}

func TestRepeatedChestOverflowCoalescesMailAndNotices(t *testing.T) {
	c, p, srv := actWorld(t, "chestmail")
	p.mu.Lock()
	p.addItem(10002, 600)
	p.addItem(10003, 600)
	p.BagLimit[bagSuit] = len(p.Suits)
	p.BagLimit[bagBook] = len(p.Books)
	p.mu.Unlock()
	notices, mailPushes := 0, 0
	var rewardGear int64
	for range 50 {
		c.sendExt("ETC_PTO.C_OPEN_TEN_BOX", xym.Msg{"type": 10002})
		for {
			main, body := c.recv()
			if main == xym.SMsg {
				t.Fatalf("chest refused: %s", body)
			}
			_, sp, m, e := xym.DecodeExt(xym.S2C, body)
			if e != nil {
				t.Fatal(e)
			}
			switch specName(sp) {
			case "TASK_PTO.S_BAG_FULL":
				notices++
			case "MAIL_PTO.S_GET_NEW_MAIL_NUM":
				mailPushes++
			case "ETC_PTO.S_REWARD_MSG":
				for _, g := range m["list"].([]xym.Msg) {
					if num(g, "rtype") == rtEquip || num(g, "rtype") == rtMagic {
						rewardGear += int64(num(g, "count"))
					}
				}
			}
			if specName(sp) == "ETC_PTO.S_REWARD_MSG" && num(m, "type") == boxNormal {
				break
			}
		}
	}
	srv.social.mu.Lock()
	defer srv.social.mu.Unlock()
	var attachments int64
	for _, m := range srv.social.Mails[p.UID] {
		for _, r := range m.Rewards {
			attachments += int64(r.R)
		}
	}
	if len(srv.social.Mails[p.UID]) >= 10 || attachments != rewardGear || notices > 2 || mailPushes >= 10 {
		t.Fatalf("overflow mail=%d attachments=%d expected=%d notices=%d pushes=%d", len(srv.social.Mails[p.UID]), attachments, rewardGear, notices, mailPushes)
	}
}

func TestOverflowRewardMergeIsBounded(t *testing.T) {
	if _, ok := mergeOverflowRewards([]MailReward{{S: "WP10002", R: math.MaxInt32}}, []MailReward{{S: "WP10002", R: 1}}); ok {
		t.Fatal("count overflow accepted")
	}
	var added []MailReward
	for i := 0; i <= maxOverflowRewards; i++ {
		added = append(added, MailReward{S: fmt.Sprint("WP", i), R: 1})
	}
	if _, ok := mergeOverflowRewards(nil, added); ok {
		t.Fatal("too many attachment types accepted")
	}
}
