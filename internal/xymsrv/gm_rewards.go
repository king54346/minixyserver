package xymsrv

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

type OpsDay struct {
	Login  bool `json:"login"`
	Active int  `json:"active"`
}
type RewardEvaluation struct {
	UID      int32  `json:"uid"`
	Name     string `json:"name"`
	Day      string `json:"day"`
	Login    bool   `json:"login"`
	Online   bool   `json:"online"`
	Active   int    `json:"active"`
	AgeDay   int    `json:"age_day"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
}
type OpsGrant struct {
	MailID int32  `json:"mail_id"`
	UID    int32  `json:"uid"`
	Day    string `json:"day"`
	Rule   string `json:"rule"`
	Time   int64  `json:"time"`
}

func (p *Player) opsActivity(t *TaskState) {
	if p.OpsDays == nil {
		return
	}
	d := p.OpsDays[t.Day]
	if d == nil {
		return
	}
	n := 0
	for _, it := range dailyItems {
		n += min(t.Today[it.event], it.max) * it.add
	}
	if d.Active != n {
		d.Active = n
		p.opsDirty = true
	}
}

func (p *Player) opsLogin(now time.Time) bool {
	day := now.Format("2006-01-02")
	if p.OpsDays == nil {
		p.OpsDays = map[string]*OpsDay{}
	}
	d := p.OpsDays[day]
	changed := d == nil || !d.Login
	if d == nil {
		d = &OpsDay{}
		p.OpsDays[day] = d
	}
	d.Login = true
	if p.Tasks != nil && p.Tasks.Day == day {
		p.opsActivity(p.Tasks)
	}
	cutoff := now.AddDate(0, 0, -31).Format("2006-01-02")
	for k := range p.OpsDays {
		if k < cutoff {
			delete(p.OpsDays, k)
			changed = true
		}
	}
	return changed
}

func (srv *Server) evaluateReward(r RewardRule, uid int32, now time.Time) RewardEvaluation {
	day := now.Format("2006-01-02")
	out := RewardEvaluation{UID: uid, Day: day}
	ro := srv.roles.Get(uid)
	if ro == nil {
		out.Reason = "角色不存在"
		return out
	}
	out.Name = ro.Name
	created := ro.Created.In(now.Location())
	a := time.Date(created.Year(), created.Month(), created.Day(), 0, 0, 0, 0, now.Location())
	b := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	out.AgeDay = int(b.Sub(a).Hours()/24) + 1
	if len(r.UIDs) > 0 && !slices.Contains(r.UIDs, uid) {
		out.Reason = "不在指定名单"
		return out
	}
	if r.NewPlayerDays > 0 && (out.AgeDay < 1 || out.AgeDay > r.NewPlayerDays) {
		out.Reason = "不在新角色福利期"
		return out
	}
	if r.StartDate != "" && day < r.StartDate || r.EndDate != "" && day > r.EndDate {
		out.Reason = "不在发奖日期范围"
		return out
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		out.Reason = "存档读取失败"
		return out
	}
	if !p.mu.TryLock() {
		out.Reason = "玩家忙，稍后重试"
		return out
	}
	if d := p.OpsDays[day]; d != nil {
		out.Login, out.Active = d.Login, d.Active
	}
	if p.Tasks != nil && p.Tasks.Day == day {
		n := 0
		for _, it := range dailyItems {
			n += min(p.Tasks.Today[it.event], it.max) * it.add
		}
		out.Active = max(out.Active, n)
	}
	p.mu.Unlock()
	if s := srv.sessionOf(uid); s != nil {
		out.Online = s.inGame.Load()
	}
	switch {
	case r.RequireLogin && !out.Login:
		out.Reason = "当天未登录（或尚无服务端登录记录）"
	case r.OnlineOnly && !out.Online:
		out.Reason = "当前不在线"
	case out.Active < r.MinActive:
		out.Reason = fmt.Sprintf("活跃度不足%d", r.MinActive)
	default:
		out.Eligible = true
		out.Reason = "符合条件"
	}
	return out
}

// Mail and its deduplication key commit in the same durable social record.
// An error restores the in-memory state as well, making retries safe.
func (srv *Server) sendOpsMail(key string, uid int32, r RewardRule, day string) (bool, int32, error) {
	so := srv.social
	so.mu.Lock()
	defer so.mu.Unlock()
	so.init()
	if so.OpsGrants == nil {
		so.OpsGrants = map[string]OpsGrant{}
	}
	if g, ok := so.OpsGrants[key]; ok {
		return false, g.MailID, nil
	}
	oldSeq, oldList := so.MailSeq, so.Mails[uid]
	so.MailSeq++
	ml := &Mail{Idx: so.MailSeq, Title: r.Reward.Title, Text: r.Reward.Text, Rewards: r.Reward.rewards(), Time: time.Now().Unix(), Star: r.Reward.Star, GrantRule: r.ID, GrantDay: day}
	list := append(slices.Clone(oldList), ml)
	// Never evict an unclaimed reward to insert a new one.
	for len(list) > maxMails {
		i := slices.IndexFunc(list, func(m *Mail) bool { return len(m.Rewards) == 0 || m.Got })
		if i < 0 {
			break
		}
		list = slices.Delete(list, i, i+1)
	}
	so.Mails[uid] = list
	so.OpsGrants[key] = OpsGrant{ml.Idx, uid, day, r.ID, ml.Time}
	b, err := json.Marshal(so)
	if err == nil && so.st != nil {
		err = so.st.SaveGlobal(string(globalSocial), b)
	}
	if err != nil {
		so.MailSeq, so.Mails[uid] = oldSeq, oldList
		delete(so.OpsGrants, key)
		return false, 0, err
	}
	return true, ml.Idx, nil
}

func ruleDue(r RewardRule, now time.Time) bool {
	if !r.Enabled {
		return false
	}
	day := now.Format("2006-01-02")
	if r.StartDate != "" && day < r.StartDate || r.EndDate != "" && day > r.EndDate {
		return false
	}
	if r.Mode == "once" && r.Date != day {
		return false
	}
	return r.Mode == "eligible" || now.Format("15:04") >= r.Time
}

func (srv *Server) rewardUIDs() []int32 {
	srv.roles.mu.RLock()
	uids := make([]int32, 0, len(srv.roles.ByUID))
	for uid := range srv.roles.ByUID {
		uids = append(uids, uid)
	}
	srv.roles.mu.RUnlock()
	slices.Sort(uids)
	return uids
}

func (srv *Server) runRewardRule(r RewardRule, now time.Time, dry bool) []RewardEvaluation {
	day := now.Format("2006-01-02")
	var rows []RewardEvaluation
	for _, uid := range srv.rewardUIDs() {
		e := srv.evaluateReward(r, uid, now)
		key := r.ID + "/" + day + "/" + fmt.Sprint(uid)
		if !dry {
			srv.social.mu.Lock()
			_, exists := srv.social.OpsGrants[key]
			srv.social.mu.Unlock()
			if exists {
				e.Eligible = false
				e.Reason = "当日已发放"
			}
		}
		rows = append(rows, e)
		if dry {
			continue
		}
		o := srv.ops
		if !e.Eligible {
			if o.last[key] != e.Reason {
				if err := o.audit(OpsEvent{Kind: "reward_check", UID: uid, Name: e.Name, Rule: r.ID, Day: day, Status: "skipped", Reason: e.Reason, Data: e}); err == nil {
					o.last[key] = e.Reason
				}
			}
			continue
		}
		if err := o.audit(OpsEvent{Kind: "reward_check", UID: uid, Name: e.Name, Rule: r.ID, Day: day, Status: "eligible", Data: e}); err != nil {
			slog.Error("GM reward audit unavailable", "err", err)
			continue
		}
		added, idx, err := srv.sendOpsMail(key, uid, r, day)
		status, reason := "sent", ""
		if err != nil {
			status, reason = "failed", err.Error()
		} else if !added {
			status = "duplicate"
		}
		if err := o.audit(OpsEvent{Kind: "reward_delivery", UID: uid, Name: e.Name, Rule: r.ID, Day: day, Status: status, Reason: reason, Data: map[string]any{"mail_id": idx, "reward": r.Reward, "reward_details": opsRewardDetails(r.Reward.rewards())}}); err != nil {
			slog.Error("GM reward result audit", "err", err)
		}
		if err == nil && added {
			if s := srv.sessionOf(uid); s != nil {
				s.sendMailNum()
			}
		}
	}
	return rows
}

func (srv *Server) runGMOps(now time.Time) {
	o := srv.ops
	if o == nil || o.startupErr != nil || !o.runMu.TryLock() {
		return
	}
	defer o.runMu.Unlock()
	if day := now.Format("2006-01-02"); o.lastDay != day {
		o.last = map[string]string{}
		o.lastDay = day
	}
	for _, r := range o.current.Load().Rules {
		if ruleDue(r, now) {
			srv.runRewardRule(r, now, false)
		}
	}
}

func (srv *Server) gmOpsLoop() {
	defer srv.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		srv.runGMOps(time.Now())
		select {
		case <-srv.opsStop:
			return
		case <-ticker.C:
		}
	}
}

func sevenDayRule() RewardRule {
	start := time.Now().AddDate(0, 0, 1)
	return RewardRule{ID: "seven_day_500k", Name: "全服七天福利", Mode: "eligible", RequireLogin: true, MinActive: 125, StartDate: start.Format("2006-01-02"), EndDate: start.AddDate(0, 0, 6).Format("2006-01-02"), Reward: MailSpec{Title: "七天福利", Text: "当天登录且活跃度达到125，发放当日50万元宝福利。未达标当天不发、不补发。", Gold: 500000, Star: true}}
}

func (srv *Server) opsResult(kind string, uid int32, version int64, data any) {
	if srv.ops == nil || !srv.cfg.EnableGMOps {
		return
	}
	name := ""
	if ro := srv.roles.Get(uid); ro != nil {
		name = ro.Name
	}
	if err := srv.ops.audit(OpsEvent{Kind: kind, UID: uid, Name: name, Day: today(), Status: "rolled", Data: map[string]any{"version": version, "result": data}}); err != nil {
		slog.Error("GM outcome audit", "kind", kind, "err", err)
	}
}

func opsRewardDetails(rewards []MailReward) []map[string]any {
	var out []map[string]any
	for _, r := range rewards {
		name := r.S
		if row, ok := opsRewardCatalog()[r.S]; ok {
			name = row.Name
		}
		out = append(out, map[string]any{"code": r.S, "name": name, "count": r.R})
	}
	return out
}

func cleanBroadcast(text string) string { return strings.Join(strings.Fields(text), " ") }
