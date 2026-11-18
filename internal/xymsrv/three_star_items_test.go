package xymsrv

import (
	"fmt"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func TestThreeStarItemsUseAndOverflow(t *testing.T) {
	for _, typ := range []int{11487, 11488, 11507, 11508} {
		for _, full := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/full=%t", typ, full), func(t *testing.T) {
				c, p, srv := actWorld(t, "staruse")
				isBook := typ == 11487 || typ == 11508
				p.mu.Lock()
				it := p.addItem(typ, 3)[0]
				books, suits := len(p.Books), len(p.Suits)
				if full {
					p.BagLimit[bagBook], p.BagLimit[bagSuit] = books, suits
				}
				p.mu.Unlock()
				// Oversized requests must consume and award only the three owned copies.
				c.sendExt("ITEM_PTO.C_USE_ITEM", xym.Msg{"pos": it.Pos, "type": typ, "use_num": 99, "choose": 0, "usetype": 1})
				got := c.until(t, "ETC_PTO.S_REWARD_MSG")
				list := got["ETC_PTO.S_REWARD_MSG"]["list"].([]xym.Msg)
				total := 0
				for _, reward := range list {
					id := num(reward, "type")
					if isBook {
						if num(reward, "rtype") != rtMagic || gamedata.Magics[int32(id)] == nil || gamedata.Magics[int32(id)].Star != 3 {
							t.Fatalf("wrong book reward: %v", reward)
						}
					} else if num(reward, "rtype") != rtEquip || gamedata.Suits[id] == nil || gamedata.Suits[id].Star != 3 {
						t.Fatalf("wrong suit reward: %v", reward)
					}
					total += num(reward, "count")
				}
				if total != 3 || p.itemCount(typ) != 0 {
					t.Fatalf("reward=%d remaining=%d", total, p.itemCount(typ))
				}
				if full {
					attachments := int32(0)
					srv.social.do(func() bool {
						for _, mail := range srv.social.Mails[p.UID] {
							for _, r := range mail.Rewards {
								attachments += r.R
							}
						}
						return false
					})
					if attachments != 3 || len(p.Books) != books || len(p.Suits) != suits {
						t.Fatalf("overflow delivery: attachments=%d", attachments)
					}
				} else if isBook && len(p.Books) != books+3 || !isBook && len(p.Suits) != suits+3 {
					t.Fatal("reward missing from bag")
				}
			})
		}
	}
}
