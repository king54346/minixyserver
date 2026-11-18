package xymsrv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

func gmHarness(t *testing.T) *Server {
	t.Helper()
	st := openTestStore(t, t.TempDir())
	a, err := OpenAccounts(st)
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenRoles(st)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.EnableGMOps = true
	cfg.GMOpsLog = filepath.Join(t.TempDir(), "ops.jsonl")
	srv := NewServer(cfg, a, r, OpenPlayers(st))
	for _, uid := range []int32{20001, 20002} {
		if _, err := r.Create(uid, 1, fmt.Sprint(uid)); err != nil {
			t.Fatal(err)
		}
		p, err := srv.players.Get(uid)
		if err != nil {
			t.Fatal(err)
		}
		p.role = r.Get(uid)
	}
	return srv
}

func gmConfig(o *GMOps) OpsSettings {
	b, _ := json.Marshal(o.current.Load())
	var c OpsSettings
	_ = json.Unmarshal(b, &c)
	return c
}

func setGMDaily(t *testing.T, srv *Server, uid int32, login, full bool) {
	t.Helper()
	p, err := srv.players.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.OpsDays = map[string]*OpsDay{today(): {Login: login}}
	p.Tasks = &TaskState{Day: today(), Today: map[string]int{}}
	if full {
		for _, it := range dailyItems {
			p.Tasks.Today[it.event] = it.max
		}
	}
	p.opsActivity(p.Tasks)
	if err := p.save(); err != nil {
		t.Fatal(err)
	}
}

func TestGMFlexibleConditionsAreIndependent(t *testing.T) {
	srv := gmHarness(t)
	r := sevenDayRule()
	r.StartDate, r.EndDate = "", ""
	if r.NewPlayerDays != 0 || r.Reward.Gold != 500000 {
		t.Fatal("seven-day template must target the whole server")
	}
	for _, tc := range []struct {
		login, full, needLogin bool
		min                    int
		want                   bool
	}{
		{false, false, false, 0, true}, {false, false, true, 0, false}, {true, false, true, 0, true},
		{false, true, false, 125, true}, {true, false, false, 125, false}, {true, true, true, 125, true}, {false, true, true, 125, false},
	} {
		setGMDaily(t, srv, 20001, tc.login, tc.full)
		r.RequireLogin, r.MinActive = tc.needLogin, tc.min
		if e := srv.evaluateReward(r, 20001, time.Now()); e.Eligible != tc.want {
			t.Fatalf("%+v: %+v", tc, e)
		}
	}
	r.RequireLogin, r.MinActive = false, 0
	r.OnlineOnly = true
	if e := srv.evaluateReward(r, 20001, time.Now()); e.Eligible {
		t.Fatal("offline-only filter ignored")
	}
	connecting := &Session{srv: srv}
	srv.online[20001] = connecting
	if e := srv.evaluateReward(r, 20001, time.Now()); e.Eligible || e.Online {
		t.Fatal("unfinished login counted as online")
	}
	connecting.sendMailNum() // Must not read the unpublished role pointer.
	connecting.inGame.Store(true)
	if e := srv.evaluateReward(r, 20001, time.Now()); !e.Eligible || !e.Online {
		t.Fatal("completed login not counted as online")
	}
	delete(srv.online, 20001)
	p, _ := srv.players.Get(20001)
	p.mu.Lock()
	e := srv.evaluateReward(r, 20001, time.Now())
	p.mu.Unlock()
	if !strings.Contains(e.Reason, "玩家忙") {
		t.Fatal(e)
	}
}

func TestGMRewardsDeduplicateAcrossRestartAndTrackSkipped(t *testing.T) {
	srv := gmHarness(t)
	setGMDaily(t, srv, 20001, true, true)
	setGMDaily(t, srv, 20002, true, false)
	r := sevenDayRule()
	r.Enabled = true
	r.StartDate, r.EndDate = today(), today()
	c := gmConfig(srv.ops)
	c.Rules = []RewardRule{r}
	if err := srv.ops.update(c); err != nil {
		t.Fatal(err)
	}
	srv.runGMOps(time.Now())
	srv.runGMOps(time.Now())
	if len(srv.social.Mails[20001]) != 1 || len(srv.social.Mails[20002]) != 0 {
		t.Fatal("duplicate or ineligible mail")
	}
	ml := srv.social.Mails[20001][0]
	if ml.GrantDay != today() || ml.GrantRule != r.ID || ml.Rewards[0].R != 500000 {
		t.Fatal(ml)
	}
	// A new server object sees durable config, mail and deduplication ledger.
	ro, err := OpenRoles(srv.players.st)
	if err != nil {
		t.Fatal(err)
	}
	other := NewServer(srv.cfg, srv.accounts, ro, OpenPlayers(srv.players.st))
	other.runGMOps(time.Now())
	if len(other.social.Mails[20001]) != 1 {
		t.Fatal("restart duplicated a grant")
	}
	log, err := os.ReadFile(srv.ops.logFile(today()))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"name":"20001"`, `"day":"` + today() + `"`, `"status":"sent"`, `"status":"skipped"`, `"gold":500000`, `"mail_id"`} {
		if !bytes.Contains(log, []byte(needle)) {
			t.Fatal("missing audit field", needle)
		}
	}
}

type gmFailStore struct {
	store.Store
	fail string
}

func (s gmFailStore) SaveGlobal(name string, b []byte) error {
	if name == s.fail {
		return errors.New("injected durable write failure")
	}
	return s.Store.SaveGlobal(name, b)
}

func TestGMGrantRollbackAndConcurrentDedup(t *testing.T) {
	srv := gmHarness(t)
	r := sevenDayRule()
	key := "rule/" + today() + "/20001"
	original := srv.social.st
	srv.social.st = gmFailStore{original, string(globalSocial)}
	if _, _, err := srv.sendOpsMail(key, 20001, r, today()); err == nil {
		t.Fatal("write failure hidden")
	}
	if len(srv.social.Mails[20001]) != 0 || len(srv.social.OpsGrants) != 0 || srv.social.MailSeq != 0 {
		t.Fatal("failed write leaked in-memory state")
	}
	srv.social.st = original
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := srv.sendOpsMail(key, 20001, r, today()); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(srv.social.Mails[20001]) != 1 {
		t.Fatal("concurrent duplicate mail")
	}
}

func TestGMOddsControlActualDrawsAndFragments(t *testing.T) {
	srv := gmHarness(t)
	c := gmConfig(srv.ops)
	c.Drops.Recruit[1] = RecruitOdds{Weights: map[int]int{3: 100}, RareBP: 0}
	c.Drops.Boxes[10002] = BoxOdds{Drops: []DropWeight{{Code: "GPSP5", Count: 2, Weight: 100}}}
	if err := srv.ops.update(c); err != nil {
		t.Fatal(err)
	}
	mall := &Mall{Pity: map[int]int{}}
	for _, id := range drawPets(srv.recruitRule(1), mall, 1000, rand.New(rand.NewPCG(1, 2))) {
		if gamedata.Pets[id].Star != 3 {
			t.Fatal("configured zero gold odds ignored")
		}
	}
	c = gmConfig(srv.ops)
	r := c.Drops.Recruit[1]
	r.Pity = 3
	c.Drops.Recruit[1] = r
	if err := srv.ops.update(c); err != nil {
		t.Fatal(err)
	}
	mall.Pity[1] = 2
	if id := drawPets(srv.recruitRule(1), mall, 1, rand.New(rand.NewPCG(1, 2)))[0]; gamedata.Pets[id].Star != 5 {
		t.Fatal("runtime pity not honored")
	}
	p, _ := srv.players.Get(20001)
	p.addItem(10002, 100)
	p.addItem(10003, 100)
	_, again := p.openBoxes(srv.boxRule(10003), 100, rand.New(rand.NewPCG(1, 2)), &bagDelta{})
	parts := int32(0)
	for _, n := range p.BookParts {
		parts += n
	}
	if parts != 200 || again != 0 || len(p.Books) != 0 || len(p.Suits) != 0 {
		t.Fatal("configured fragment-only box did not apply")
	}
	if boxKinds[10002].drops[0].code != "GPFS2" {
		t.Fatal("live config mutated global defaults")
	}
}

func TestGMOddsValidationAndImmutableUpdates(t *testing.T) {
	srv := gmHarness(t)
	c := gmConfig(srv.ops)
	c.Drops.Recruit[1] = RecruitOdds{Weights: map[int]int{3: 0}}
	if err := srv.ops.update(c); err == nil {
		t.Fatal("zero-total weights accepted")
	}
	c = gmConfig(srv.ops)
	if err := srv.ops.update(c); err != nil {
		t.Fatal(err)
	}
	c.Drops.Recruit[1].Weights[5] = 99999
	if srv.recruitRule(1).rates[5] != 3 {
		t.Fatal("request map still aliases published config")
	}
	if err := srv.ops.update(c); err == nil {
		t.Fatal("stale version accepted")
	}
	c = gmConfig(srv.ops)
	c.Drops.Boxes[10002] = BoxOdds{Drops: []DropWeight{{Code: "WP99999999", Count: 1, Weight: 1}}}
	if err := srv.ops.update(c); err == nil {
		t.Fatal("nonexistent drop accepted")
	}
	old := srv.ops.current.Load().Version
	srv.ops.st = gmFailStore{srv.players.st, globalGMOps}
	c = gmConfig(srv.ops)
	if err := srv.ops.update(c); err == nil || srv.ops.current.Load().Version != old {
		t.Fatal("failed config save was published")
	}
}

func TestGMRuleTimeWindows(t *testing.T) {
	now := time.Date(2026, 11, 1, 20, 59, 0, 0, time.FixedZone("CST", 8*3600))
	r := sevenDayRule()
	r.Enabled = true
	r.Mode = "daily"
	r.Time = "21:00"
	r.StartDate = "2026-11-01"
	r.EndDate = "2026-11-07"
	if ruleDue(r, now) || !ruleDue(r, now.Add(time.Minute)) || ruleDue(r, now.AddDate(0, 0, 7)) {
		t.Fatal("daily/7-day date window")
	}
	r.Mode = "once"
	r.Date = "2026-11-02"
	if ruleDue(r, now.Add(time.Minute)) {
		t.Fatal("once date ignored")
	}
	r.Mode = "eligible"
	if !ruleDue(r, now) {
		t.Fatal("eligible mode incorrectly waits for time")
	}
	r.Enabled = false
	if ruleDue(r, now) {
		t.Fatal("disabled rule executed")
	}
}

func TestGMPreviewLogsAndManualRunRoutes(t *testing.T) {
	srv := gmHarness(t)
	r := sevenDayRule()
	r.StartDate, r.EndDate = "", ""
	r.RequireLogin, r.MinActive = false, 0
	r.Enabled = true
	c := gmConfig(srv.ops)
	c.Rules = []RewardRule{r}
	if err := srv.ops.update(c); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		srv.AdminHandler().ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(b)))
		return w
	}
	if w := call("POST", "/gm/preview", r); w.Code != 200 || len(srv.social.Mails[20001]) != 0 {
		t.Fatal("preview sends mail", w.Body.String())
	}
	for range 2 {
		if w := call("POST", "/gm/run", map[string]string{"id": r.ID}); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if len(srv.social.Mails[20001]) != 1 || len(srv.social.Mails[20002]) != 1 {
		t.Fatal("unconditional or duplicate run failed")
	}
	w := call("GET", "/gm/logs?day="+today()+"&uid=20001&kind=reward_delivery", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"uid":20001`) || strings.Contains(w.Body.String(), `"uid":20002`) {
		t.Fatal(w.Body.String())
	}
	if w := call("GET", "/gm/logs?day=../../etc/passwd", nil); w.Code != 400 {
		t.Fatal("invalid log path accepted")
	}
}

func TestGMLoginObservationAndClaimAudit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableGMOps = true
	cfg.GMOpsLog = filepath.Join(t.TempDir(), "ops.jsonl")
	srv, addr, _ := startServerSrv(t, cfg)
	c := enterWorld(t, addr, "auditclaim")
	acc, _ := srv.accounts.Login("auditclaim", "pw", "")
	p, _ := srv.players.Get(acc.UID)
	p.mu.Lock()
	login := p.OpsDays[today()] != nil && p.OpsDays[today()].Login
	p.mu.Unlock()
	if !login {
		t.Fatal("real login was not observed")
	}
	r := sevenDayRule()
	r.RequireLogin, r.MinActive = false, 0
	r.StartDate, r.EndDate = "", ""
	_, idx, err := srv.sendOpsMail("claim/"+today(), acc.UID, r, today())
	if err != nil {
		t.Fatal(err)
	}
	c.sendExt("MAIL_PTO.C_CLICK_NEW_MAIL_REWARD", xym.Msg{"idx": idx})
	c.until(t, "MAIL_PTO.S_CLICK_NEW_MAIL_REWARD_SUCC")
	log, err := os.ReadFile(srv.ops.logFile(today()))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"kind":"mail_claim"`, `"name":"auditclaim"`, `"grant_day":"` + today() + `"`, `"count":500000`, `"grant_rule":"seven_day_500k"`} {
		if !bytes.Contains(log, []byte(needle)) {
			t.Fatal("missing claim audit field", needle, string(log))
		}
	}
}

func TestGMBroadcastChannelsAndAudit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableGMOps = true
	cfg.GMOpsLog = filepath.Join(t.TempDir(), "ops.jsonl")
	srv, addr, _ := startServerSrv(t, cfg)
	a := enterWorld(t, addr, "broadcast_a")
	b := enterWorld(t, addr, "broadcast_b")
	_ = dial(t, addr) // A connected socket without a completed login is not a recipient.
	w := httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, httptest.NewRequest("POST", "/broadcast", strings.NewReader(`{"text":"  测试公告\n新一行  ","popup":true,"chat":true}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sent":2`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range []*client{a, b} {
		if m := c.until(t, "ETC_PTO.S_SEND_NO_CACHE_MSG")["ETC_PTO.S_SEND_NO_CACHE_MSG"]; m["msg"] != "测试公告 新一行" {
			t.Fatal(m)
		}
		if m := c.until(t, "CHAT_PTO.S_CHANNEL_MSG")["CHAT_PTO.S_CHANNEL_MSG"]; m["msg"] != "测试公告 新一行" || m["name"] != "系统" {
			t.Fatal(m)
		}
	}
	srv.chat.Lock()
	history := len(srv.chat.Hist[histKey(chWorld, 0)])
	srv.chat.Unlock()
	if history != 1 {
		t.Fatal("system chat history missing")
	}
	w = httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, httptest.NewRequest("POST", "/broadcast", strings.NewReader(`{"text":"invalid","popup":false,"chat":false}`)))
	if w.Code != 400 {
		t.Fatal("broadcast without channels accepted")
	}
	log, err := os.ReadFile(srv.ops.logFile(today()))
	if err != nil || !bytes.Contains(log, []byte(`"recipients":2`)) {
		t.Fatal("broadcast audit missing", err)
	}
}

func TestGMBoxDropsPersistAcrossReload(t *testing.T) {
	srv := gmHarness(t)
	original := srv.boxRule(10002)
	if err := srv.boxSet(10002, []boxDrop{{123, "ZFBOOT", 2}, {5, "WP10015", 1}}); err != nil {
		t.Fatal(err)
	}
	srv.ops = newGMOps(srv.players.st, "")
	got := srv.boxRule(10002)
	if srv.ops.startupErr != nil || got.drops[0] != (boxDrop{123, "ZFBOOT", 2}) || got.again != original.again {
		t.Fatalf("reloaded box = %+v, load error = %v", got, srv.ops.startupErr)
	}
	if err := srv.boxReset(10002); err != nil {
		t.Fatal(err)
	}
	srv.ops = newGMOps(srv.players.st, "")
	got = srv.boxRule(10002)
	if srv.ops.startupErr != nil || len(got.drops) != len(original.drops) || got.drops[0] != original.drops[0] {
		t.Fatalf("reloaded preset = %+v, load error = %v", got, srv.ops.startupErr)
	}
}
