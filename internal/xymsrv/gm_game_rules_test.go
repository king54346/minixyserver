package xymsrv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
)

func TestGameRulesPersistValidateAndApplyAtStartup(t *testing.T) {
	srv := gmHarness(t)
	handler := srv.AdminHandler()
	before := itemShop[0].Price
	daily := ldDailyTimes
	talent := gamedata.Talents[20011]
	oldParam := talent.Param
	exchangePrice := ldShop[0].price
	t.Cleanup(func() {
		itemShop[0].Price = before
		ldDailyTimes = daily
		talent.Param = oldParam
		ldShop[0].price = exchangePrice
	})
	query := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(raw)))
		return w
	}
	w := query("GET", "/gm/game-rules?group=shop", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var response struct {
		Settings gameRuleConfig
		Rows     []gameRuleRow
	}
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
	if len(response.Rows) != len(itemShop) {
		t.Fatal("missing shop rows")
	}
	c := response.Settings
	c.Changes["shop/11153"] = map[string]json.RawMessage{"Price": json.RawMessage("123")}
	c.Changes["counts/lundao_daily"] = map[string]json.RawMessage{"Value": json.RawMessage("21")}
	c.Changes["exchange/11118"] = map[string]json.RawMessage{"Price": json.RawMessage("900")}
	c.Changes["talents/20011"] = map[string]json.RawMessage{"Param": json.RawMessage("[22]")}
	w = query("POST", "/gm/game-rules", c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if itemShop[0].Price != before || ldDailyTimes != daily || talent.Param[0] != oldParam[0] {
		t.Fatal("save mutated live gameplay")
	}
	if w = query("POST", "/gm/game-rules", c); w.Code != 409 {
		t.Fatal("stale settings accepted", w.Code)
	}
	c.Version++
	bad := cloneRuleConfig(c)
	bad.Changes["shop/11153"]["Price"] = json.RawMessage("-1")
	if w = query("POST", "/gm/game-rules", bad); w.Code != 400 {
		t.Fatal("negative price accepted")
	}
	bad = cloneRuleConfig(c)
	bad.Changes["talents/20011"]["FamilyID"] = json.RawMessage("0")
	if w = query("POST", "/gm/game-rules", bad); w.Code != 400 {
		t.Fatal("skill implementation identity accepted")
	}
	bad = cloneRuleConfig(c)
	var rewardID int32
	for id := range gamedata.Rewards {
		rewardID = id
		break
	}
	bad.Changes[fmt.Sprintf("rewards/%d", rewardID)] = map[string]json.RawMessage{"Drops": json.RawMessage(`["WP10126:20000:5"]`)}
	if w = query("POST", "/gm/game-rules", bad); w.Code != 400 {
		t.Fatal("invalid drop accepted")
	}
	bad = cloneRuleConfig(c)
	bad.Changes["magics/14507"] = map[string]json.RawMessage{"Hurt": json.RawMessage("[1,2,3]")}
	if w = query("POST", "/gm/game-rules", bad); w.Code != 400 {
		t.Fatal("oversized coefficient array accepted")
	}
	st := srv.gameRules.st
	srv.gameRules.st = failGameRulesSave{st}
	if w = query("POST", "/gm/game-rules", c); w.Code != 500 {
		t.Fatal("failed save reported success")
	}
	if srv.gameRules.pending.Version != c.Version {
		t.Fatal("failed save advanced version")
	}
	srv.gameRules.st = st
	// Reopening the actual durable store simulates process startup.
	next := newGameRules(srv.players.st)
	if next.err != nil {
		t.Fatal(next.err)
	}
	if itemShop[0].Price != 123 || ldDailyTimes != 21 || talent.Param[0] != 22 || ldShop[0].price != 900 {
		t.Fatal("pending configuration not applied on startup")
	}
	if next.active.Version != c.Version {
		t.Fatal("version not retained")
	}
	// Each server's global record is independent, including reward timers.
	other := newGameRules(store.OpenFile(t.TempDir()))
	if len(other.pending.Changes) != 0 {
		t.Fatal("configuration leaked to another store")
	}
}

type failGameRulesSave struct{ store.Store }

func (f failGameRulesSave) SaveGlobal(string, []byte) error {
	return errors.New("injected storage failure")
}
