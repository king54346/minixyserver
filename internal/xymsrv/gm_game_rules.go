package xymsrv

// Gameplay tables are shared with battle and player attribute calculations.
// Publish edits only at process startup, before accepting connections, rather
// than mutating maps underneath an in-flight fight. Pending edits are durable.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/store"
)

const globalGameRules = "gm_game_rules"

type gameRuleConfig struct {
	Version int64                                 `json:"version"`
	Changes map[string]map[string]json.RawMessage `json:"changes"`
}
type gameRuleRow struct {
	Key      string         `json:"key"`
	Group    string         `json:"group"`
	Name     string         `json:"name"`
	Values   map[string]any `json:"values"`
	Defaults map[string]any `json:"defaults"`
	fields   map[string]reflect.Value
	setters  map[string]func(int64)
}
type gameRules struct {
	mu      sync.Mutex
	once    sync.Once
	st      store.Store
	pending gameRuleConfig
	active  gameRuleConfig
	rows    map[string]*gameRuleRow
	err     error
}

func newGameRules(st store.Store) *gameRules {
	g := &gameRules{st: st, pending: gameRuleConfig{Version: 1, Changes: map[string]map[string]json.RawMessage{}}}
	if st != nil {
		raw, err := st.LoadGlobal(globalGameRules)
		if err == nil {
			g.err = json.Unmarshal(raw, &g.pending)
		} else if !errors.Is(err, store.ErrNotFound) {
			g.err = err
		}
	}
	if g.pending.Changes == nil {
		g.pending.Changes = map[string]map[string]json.RawMessage{}
	}
	if g.err == nil && len(g.pending.Changes) > 0 {
		g.init()
		if g.err = g.validate(g.pending); g.err == nil {
			for key, fields := range g.pending.Changes {
				for name, raw := range fields {
					row := g.rows[key]
					dst := row.fields[name]
					v := reflect.New(dst.Type())
					_ = json.Unmarshal(raw, v.Interface())
					dst.Set(v.Elem())
					if set := row.setters[name]; set != nil {
						set(v.Elem().Int())
					}
				}
			}
		}
	}
	g.active = cloneRuleConfig(g.pending)
	return g
}
func cloneRuleConfig(c gameRuleConfig) gameRuleConfig {
	b, _ := json.Marshal(c)
	var out gameRuleConfig
	_ = json.Unmarshal(b, &out)
	return out
}
func (g *gameRules) add(group, id, name string, ptr any, fields ...string) {
	v := reflect.ValueOf(ptr).Elem()
	row := &gameRuleRow{Key: group + "/" + id, Group: group, Name: name, Values: map[string]any{}, fields: map[string]reflect.Value{}}
	for _, field := range fields {
		f := v.FieldByName(field)
		if !f.IsValid() || !f.CanSet() {
			panic("invalid GM field " + field)
		}
		row.fields[field] = f
	}
	g.rows[row.Key] = row
}
func (g *gameRules) init() {
	g.once.Do(func() {
		g.rows = map[string]*gameRuleRow{}
		for id, x := range gamedata.Gates {
			for mode := range x.Mode {
				g.add("gates", fmt.Sprintf("%d-%d", id, mode), fmt.Sprintf("%s · %s", x.Name, []string{"普通", "精英"}[mode]), &x.Mode[mode], "Cost", "Param", "Limit", "Reward")
			}
		}
		for id, x := range gamedata.Rewards {
			g.add("rewards", strconv.Itoa(int(id)), x.Desc, x, "Drops", "Soul", "CashA", "CashB", "MExpA", "MExpB", "PExpA", "PExpB")
		}
		for id, x := range gamedata.Talents {
			g.add("talents", strconv.Itoa(int(id)), x.Family, x, "Param", "TJParam", "BuffRate", "Need")
		}
		for id, x := range gamedata.Magics {
			g.add("magics", strconv.Itoa(int(id)), x.Name, x, "Hurt", "CostZY", "BuffRate", "Rate", "Times", "CondZY", "JXCost")
		}
		for i := range itemShop {
			x := &itemShop[i]
			g.add("shop", strconv.Itoa(x.Type), gamedata.Items[x.Type].Name, x, "Price", "Limit")
		}
		for i := range gamedata.Dangyao.Shop {
			x := &gamedata.Dangyao.Shop[i]
			g.add("dangyao", fmt.Sprintf("%d-%d", x.Type, x.Pos), x.Name, x, "Price", "Left", "Need", "Weight", "CA")
		}
		for i := range ldShop {
			x := &ldShop[i] // Unexported runtime rows require explicit exported adapters below.
			idx := i
			g.addAdapter("exchange", strconv.Itoa(int(x.cid)), x.code, map[string]*int64Adapter{
				"Count": newIntAdapter(func() int64 { return int64(ldShop[idx].n) }, func(v int64) { ldShop[idx].n = int32(v) }),
				"Price": newIntAdapter(func() int64 { return int64(ldShop[idx].price) }, func(v int64) { ldShop[idx].price = int32(v) }),
				"Grade": newIntAdapter(func() int64 { return int64(ldShop[idx].grade) }, func(v int64) { ldShop[idx].grade = int(v) }),
				"Limit": newIntAdapter(func() int64 { return int64(ldShop[idx].times) }, func(v int64) { ldShop[idx].times = int(v) }),
			})
		}
		for id, x := range gamedata.CardFrames {
			g.add("frames", strconv.Itoa(id), x.Name, x, "LastSec", "AttAdd", "HPAdd")
		}
		for id := range dfShopPrice {
			key := id
			g.addAdapter("dongfu_shop", strconv.Itoa(id), fmt.Sprintf("%d星仙灵竞拍底价（灵气）", id), map[string]*int64Adapter{"Price": newIntAdapter(func() int64 { return int64(dfShopPrice[key]) }, func(v int64) { dfShopPrice[key] = int32(v) })})
		}
		for id, pool := range recruitPools {
			g.addAdapter("recruit_cost", strconv.Itoa(id), pool.name, map[string]*int64Adapter{
				"Cash":        newIntAdapter(func() int64 { return int64(pool.cash) }, func(v int64) { pool.cash = int32(v) }),
				"Friend":      newIntAdapter(func() int64 { return int64(pool.friend) }, func(v int64) { pool.friend = int32(v) }),
				"FreeSeconds": newIntAdapter(func() int64 { return int64(pool.freeEvery / time.Second) }, func(v int64) { pool.freeEvery = time.Duration(v) * time.Second }),
			})
		}
		for i := range ldScoreCfg {
			idx := i
			g.addAdapter("lundao_score", strconv.Itoa(ldScoreCfg[i].rank), "名次以内每小时积分（30／40／50级档）", map[string]*int64Adapter{
				"Score30": newIntAdapter(func() int64 { return int64(ldScoreCfg[idx].score[0]) }, func(v int64) { ldScoreCfg[idx].score[0] = int32(v) }),
				"Score40": newIntAdapter(func() int64 { return int64(ldScoreCfg[idx].score[1]) }, func(v int64) { ldScoreCfg[idx].score[1] = int32(v) }),
				"Score50": newIntAdapter(func() int64 { return int64(ldScoreCfg[idx].score[2]) }, func(v int64) { ldScoreCfg[idx].score[2] = int32(v) }),
			})
		}
		g.addCount("lundao_daily", "论道每日挑战次数", &ldDailyTimes)
		g.addCount32("lundao_buy_gold", "论道额外挑战价格", &ldBuyGold)
		g.addCount("tianti_daily", "天梯每日挑战次数", &ttDayLimit)
		g.addCount("tianti_win_gold", "天梯胜利元宝", &ttWinGold)
		g.addCount("tianti_lose_gold", "天梯失败元宝", &ttLoseGold)
		g.addCount("recruit_cash_free", "银两招募每日免费次数", &cashFreePerDay)
		g.addCount("recruit_cash_paid", "银两招募每日付费次数", &cashPaidPerDay)
		g.addCount("draft_daily", "轮抓每日挑战次数", &draftFightLimit)
		g.addCount("draft_skips", "轮抓每日跳过次数", &draftSkipLimit)
		g.addCount32("draft_redraft_gold", "轮抓重抓价格（首次仍免费）", &draftRedraftGold)
		for _, row := range g.rows {
			row.Defaults = map[string]any{}
			for name, v := range row.fields {
				row.Defaults[name] = v.Interface()
			}
		}
	})
}

// Adapters retain type safety without exporting internal economy table fields.
type int64Adapter struct {
	Value int64
	set   func(int64)
}

func newIntAdapter(get func() int64, set func(int64)) *int64Adapter {
	return &int64Adapter{Value: get(), set: set}
}
func (g *gameRules) addAdapter(group, id, name string, fields map[string]*int64Adapter) {
	row := &gameRuleRow{Key: group + "/" + id, Group: group, Name: name, fields: map[string]reflect.Value{}, setters: map[string]func(int64){}}
	for k, a := range fields {
		v := reflect.ValueOf(a).Elem().FieldByName("Value")
		row.fields[k] = v
		row.setters[k] = a.set
	}
	g.rows[row.Key] = row
}
func (g *gameRules) addCount(id, name string, p *int) {
	g.addAdapter("counts", id, name, map[string]*int64Adapter{"Value": newIntAdapter(func() int64 { return int64(*p) }, func(v int64) { *p = int(v) })})
}
func validateRuleValue(field string, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64:
		n := v.Int()
		min := int64(0)
		if field == "Left" || field == "LastSec" || field == "Times" {
			min = -1
		}
		if n < min || n > 10000000 {
			return errors.New("整数超出允许范围")
		}
		if (field == "Rate" || field == "BuffRate") && n > 100 {
			return errors.New("百分比范围为0至100")
		}
	case reflect.Float64:
		if v.Float() < -1000000 || v.Float() > 1000000 {
			return errors.New("参数超出允许范围")
		}
	case reflect.Slice, reflect.Array:
		if v.Len() > 100 {
			return errors.New("数组过长")
		}
		for i := 0; i < v.Len(); i++ {
			if err := validateRuleValue(field, v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if field != "Drops" {
			return errors.New("该文本不能修改")
		}
		for _, code := range strings.Split(v.String(), ",") {
			parts := strings.Split(code, ":")
			if len(parts) != 3 || !validBoxRewardCode(parts[0]) {
				return errors.New("掉落格式应为 编码:万分比:数量")
			}
			bp, e := strconv.Atoi(parts[1])
			n, e2 := strconv.Atoi(parts[2])
			if e != nil || e2 != nil || bp < 0 || bp > 10000 || n < 1 || n > 100000 {
				return errors.New("掉落概率或数量无效")
			}
		}
	default:
		return errors.New("不支持的参数类型")
	}
	return nil
}
func (g *gameRules) validate(c gameRuleConfig) error {
	if c.Version < 1 || len(c.Changes) > 20000 {
		return errors.New("配置版本或条数无效")
	}
	for key, fields := range c.Changes {
		row := g.rows[key]
		if row == nil {
			return fmt.Errorf("配置项不存在: %s", key)
		}
		for name, raw := range fields {
			dst, ok := row.fields[name]
			if !ok {
				return fmt.Errorf("不可调整的字段: %s/%s", key, name)
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return errors.New("配置值不能为空")
			}
			v := reflect.New(dst.Type())
			if dst.Kind() == reflect.Array {
				var elements []json.RawMessage
				if json.Unmarshal(raw, &elements) != nil || len(elements) != dst.Len() {
					return fmt.Errorf("%s/%s: 必须包含%d项", key, name, dst.Len())
				}
			}
			if err := json.Unmarshal(raw, v.Interface()); err != nil {
				return fmt.Errorf("%s/%s: %w", key, name, err)
			}
			if err := validateRuleValue(name, v.Elem()); err != nil {
				return fmt.Errorf("%s/%s: %w", key, name, err)
			}
			if name == "Count" && v.Elem().Int() == 0 {
				return errors.New("每份奖励数量必须大于0")
			}
			if strings.HasPrefix(key, "counts/") && !strings.Contains(key, "gold") && v.Elem().Int() > 100 {
				return errors.New("每日次数上限为100")
			}
			if row.Group == "gates" && name == "Cost" && v.Elem().Int() > 100 {
				return errors.New("单次体力消耗上限为100")
			}
			if name == "Reward" || name == "CA" {
				if v.Elem().Int() != 0 && gamedata.Rewards[int32(v.Elem().Int())] == nil {
					return errors.New("奖励编号不存在")
				}
			}
		}
	}
	return nil
}
func (srv *Server) gmGameRuleHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /gm/game-rules", func(w http.ResponseWriter, r *http.Request) {
		g := srv.gameRules
		g.mu.Lock()
		defer g.mu.Unlock()
		g.init()
		if g.err != nil {
			writeJSON(w, 500, map[string]string{"error": g.err.Error()})
			return
		}
		group, q := r.URL.Query().Get("group"), strings.ToLower(r.URL.Query().Get("q"))
		var keys []string
		groups := map[string]int{}
		for k, row := range g.rows {
			groups[row.Group]++
			if (group == "" || row.Group == group) && strings.Contains(strings.ToLower(k+" "+row.Name), q) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		offset = min(max(offset, 0), len(keys))
		end := min(offset+50, len(keys))
		rows := []gameRuleRow{}
		for _, key := range keys[offset:end] {
			row := *g.rows[key]
			row.Values = map[string]any{}
			for f, v := range row.fields {
				row.Values[f] = v.Interface()
			}
			rows = append(rows, row)
		}
		writeJSON(w, 200, map[string]any{"settings": g.pending, "active": g.active, "groups": groups, "rows": rows, "total": len(keys), "offset": offset, "restart_required": !reflect.DeepEqual(g.pending.Changes, g.active.Changes), "effect": "保存后重启游戏服务生效；客户端固定文案需配套更新"})
	})
	mux.HandleFunc("POST /gm/game-rules", func(w http.ResponseWriter, r *http.Request) {
		g := srv.gameRules
		g.mu.Lock()
		defer g.mu.Unlock()
		g.init()
		var c gameRuleConfig
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if g.err != nil {
			writeJSON(w, 500, map[string]string{"error": g.err.Error()})
			return
		}
		if c.Version != g.pending.Version {
			writeJSON(w, 409, map[string]string{"error": "配置已被其他窗口修改，请重新读取"})
			return
		}
		if err := g.validate(c); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		c.Version++
		if c.Changes == nil {
			c.Changes = map[string]map[string]json.RawMessage{}
		}
		raw, _ := json.Marshal(c)
		if err := srv.ops.audit(OpsEvent{Kind: "game_rules", Status: "requested", Data: c}); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if g.st == nil {
			writeJSON(w, 500, map[string]string{"error": "持久化存储不可用"})
			return
		}
		if err := g.st.SaveGlobal(globalGameRules, raw); err != nil {
			writeJSON(w, 500, map[string]string{"error": "配置保存失败"})
			return
		}
		g.pending = cloneRuleConfig(c)
		writeJSON(w, 200, map[string]any{"settings": g.pending, "restart_required": !reflect.DeepEqual(g.pending.Changes, g.active.Changes)})
	})
}

func (g *gameRules) addCount32(id, name string, p *int32) {
	g.addAdapter("counts", id, name, map[string]*int64Adapter{"Value": newIntAdapter(func() int64 { return int64(*p) }, func(v int64) { *p = int32(v) })})
}
