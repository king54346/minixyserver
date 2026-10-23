package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// TestNewbieRulesCover：任务表里每个任务名都有对应的完成条件（首充除外）。
func TestNewbieRulesCover(t *testing.T) {
	for _, task := range gamedata.NewbieTasks {
		ok := false
		for _, r := range newbieRules {
			if r.re.MatchString(task.Name) {
				ok = true
				break
			}
		}
		if !ok && task.Name != "完成首充" {
			t.Errorf("no rule for task %d %s", task.ID, task.Name)
		}
	}
}

// TestDailyActive：做 3 次关卡（各 2 活跃）+ 5 次招募（各 2）… 活跃度累加；满 25 领第一个宝箱；不够领下一个时拒绝。
func TestDailyActive(t *testing.T) {
	c, p, srv := actWorld(t, "daily")
	p.mu.Lock()
	p.track(evGate, 15)   // 封顶 10 次 × 2 = 20
	p.track(evRecruit, 3) // 3 × 2 = 6
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_APPLY_DAILY", nil)
	m := c.expectExt("ETC_PTO.S_SEND_DAILY")
	if num(m, "active_point") != 26 || len(m["list"].([]xym.Msg)) != len(dailyItems) {
		t.Fatalf("daily %v", m)
	}
	cash := srv.roles.Get(p.UID).Cash
	c.sendExt("ETC_PTO.C_GET_ACTIVE_REWARD", nil)
	got := c.until(t, "ETC_PTO.S_SEND_DAILY")
	if num(got["ETC_PTO.S_SEND_DAILY"], "reward") != 1 || srv.roles.Get(p.UID).Cash != cash+10000 || p.itemCount(10006) < 3 {
		t.Fatalf("chest %v cash %d", got["ETC_PTO.S_SEND_DAILY"], srv.roles.Get(p.UID).Cash)
	}
	c.sendExt("ETC_PTO.C_GET_ACTIVE_REWARD", nil)
	c.expectExt("ETC_PTO.S_RET_MSG")
}

// TestNewbieTask：「通关关卡1-1」打完那一任务的所有关卡后可领，领后元宝增加、该链出下一个任务。
func TestNewbieTask(t *testing.T) {
	c, p, srv := actWorld(t, "newbie")
	var first gamedata.NewbieTask
	for _, task := range gamedata.NewbieTasks {
		if task.Name == "通关关卡1-1" {
			first = task
		}
	}
	p.mu.Lock()
	for _, gid := range gamedata.TaskGates[101] {
		p.clearGate(0, gid)
	}
	p.mu.Unlock()
	c.sendExt("ETC_PTO.C_APPLY_DAILY", nil)
	var entry xym.Msg
	for _, x := range c.expectExt("ETC_PTO.S_SEND_DAILY")["newbie_task_list"].([]xym.Msg) {
		if num(x, "taskid") == int(first.ID) {
			entry = x
		}
	}
	if entry == nil || num(entry, "state") != 2 {
		t.Fatalf("task 1-1 entry %v", entry)
	}
	gold := srv.roles.Get(p.UID).Gold
	c.sendExt("ETC_PTO.C_GET_NEWBIE_TASK_REWARD", xym.Msg{"taskid": first.ID})
	got := c.until(t, "ETC_PTO.S_SEND_DAILY")
	if first.Gold > 0 && srv.roles.Get(p.UID).Gold != gold+first.Gold {
		t.Fatalf("gold %d -> %d want +%d", gold, srv.roles.Get(p.UID).Gold, first.Gold)
	}
	for _, x := range got["ETC_PTO.S_SEND_DAILY"]["newbie_task_list"].([]xym.Msg) {
		if num(x, "taskid") == int(first.ID) {
			t.Fatal("claimed task still listed")
		}
		if num(x, "taskid") == int(first.ID)+1 {
			return
		}
	}
	t.Fatal("next gate task not listed")
}
