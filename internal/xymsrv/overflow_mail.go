package xymsrv

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"
)

const overflowMailTitle = "背包已满"
const overflowMailText = "背包已满，放不下的物品已通过邮件寄给你，清理背包后在邮件里领取。"
const maxOverflowRewards = 128

func mergeOverflowRewards(old, added []MailReward) ([]MailReward, bool) {
	counts := map[string]int64{}
	var order []string
	for _, list := range [][]MailReward{old, added} {
		for _, r := range list {
			if r.R <= 0 {
				continue
			}
			if _, exists := counts[r.S]; !exists {
				order = append(order, r.S)
			}
			counts[r.S] += int64(r.R)
			if counts[r.S] > math.MaxInt32 {
				return nil, false
			}
		}
	}
	if len(order) > maxOverflowRewards {
		return nil, false
	}
	out := make([]MailReward, 0, len(order))
	for _, code := range order {
		out = append(out, MailReward{S: code, R: int32(counts[code])})
	}
	return out, true
}

// Repeated chest overflows update an unclaimed overflow mail instead of adding
// one mail and one notification for every ten boxes. Commit before notifying.
func (srv *Server) sendOverflowMail(uid int32, items map[string]int32) (bool, error) {
	if srv.roles.Get(uid) == nil {
		return false, fmt.Errorf("no role %d", uid)
	}
	added := (MailSpec{Items: items}).rewards()
	so := srv.social
	so.mu.Lock()
	so.init()
	oldList, oldSeq := so.Mails[uid], so.MailSeq
	list := slices.Clone(oldList)
	notify := false
	for from := 0; from < len(added); from += maxOverflowRewards {
		chunk := added[from:min(from+maxOverflowRewards, len(added))]
		merged := false
		for i := len(list) - 1; i >= 0; i-- {
			m := list[i]
			if m.Got || m.Star || m.Title != overflowMailTitle || m.Text != overflowMailText {
				continue
			}
			rewards, ok := mergeOverflowRewards(m.Rewards, chunk)
			if !ok {
				continue
			}
			copy := *m
			copy.Rewards, copy.Read = rewards, false
			list[i] = &copy
			notify = notify || m.Read
			merged = true
			break
		}
		if !merged {
			so.MailSeq++
			list = append(list, &Mail{Idx: so.MailSeq, Title: overflowMailTitle, Text: overflowMailText, Rewards: chunk, Time: time.Now().Unix()})
			notify = true
		}
	}
	// Never discard an unclaimed attachment to make room for overflow loot.
	for len(list) > maxMails {
		i := slices.IndexFunc(list, func(m *Mail) bool { return len(m.Rewards) == 0 || m.Got })
		if i < 0 {
			break
		}
		list = slices.Delete(list, i, i+1)
	}
	so.Mails[uid] = list
	data, err := json.Marshal(so)
	if err == nil {
		err = so.st.SaveGlobal(string(globalSocial), data)
	}
	if err != nil {
		so.Mails[uid], so.MailSeq = oldList, oldSeq
	}
	so.mu.Unlock()
	if err != nil {
		return false, err
	}
	// The request's own session sends this notification. During SID reconnect,
	// the session currently in online may still be loading its role.
	return notify, nil
}
