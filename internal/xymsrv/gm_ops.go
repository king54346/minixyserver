package xymsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"turnrpg/internal/store"
)

const globalGMOps = "gm_operations"

type RecruitOdds struct {
	Weights map[int]int `json:"weights"`
	Pity    int         `json:"pity"`
	TenMin  int         `json:"ten_min"`
	RareBP  int         `json:"rare_bp"` // Independent daily rare roll, per 10000.
}
type BoxOdds struct {
	Drops   []DropWeight `json:"drops"`
	AgainBP int          `json:"again_bp"`
}
type DropWeight struct {
	Code   string `json:"code"`
	Count  int32  `json:"count"`
	Weight int    `json:"weight"`
}
type DropSettings struct {
	Recruit map[int]RecruitOdds `json:"recruit"`
	Boxes   map[int]BoxOdds     `json:"boxes"`
}
type RewardRule struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	Mode          string   `json:"mode"` // daily at HH:mm, eligible whenever conditions pass, once on Date.
	Time          string   `json:"time"`
	Date          string   `json:"date,omitempty"`
	StartDate     string   `json:"start_date,omitempty"`
	EndDate       string   `json:"end_date,omitempty"`
	RequireLogin  bool     `json:"require_login"`
	OnlineOnly    bool     `json:"online_only"`
	MinActive     int      `json:"min_active"`
	NewPlayerDays int      `json:"new_player_days"`
	UIDs          []int32  `json:"uids,omitempty"`
	Reward        MailSpec `json:"reward"`
}
type OpsSettings struct {
	Version int64        `json:"version"`
	Drops   DropSettings `json:"drops"`
	Rules   []RewardRule `json:"rules"`
}
type OpsEvent struct {
	Time   string `json:"time"`
	Kind   string `json:"kind"`
	UID    int32  `json:"uid,omitempty"`
	Name   string `json:"name,omitempty"`
	Rule   string `json:"rule,omitempty"`
	Day    string `json:"day,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Data   any    `json:"data,omitempty"`
}
type GMOps struct {
	mu         sync.Mutex
	runMu      sync.Mutex
	journalMu  sync.Mutex
	st         store.Store
	path       string
	current    atomic.Pointer[OpsSettings] // Immutable, never modified after publication.
	recent     []OpsEvent
	last       map[string]string // Suppress identical eligibility skips within the process.
	lastDay    string
	startupErr error
}

func defaultOpsSettings() OpsSettings {
	d := DropSettings{Recruit: map[int]RecruitOdds{}, Boxes: map[int]BoxOdds{}}
	for id, p := range recruitPools {
		weights := map[int]int{}
		for star, w := range p.rates {
			weights[star] = w
		}
		d.Recruit[id] = RecruitOdds{Weights: weights, Pity: p.pity, TenMin: p.tenMin, RareBP: rarePct * 100}
	}
	for id, b := range boxKinds {
		r := BoxOdds{AgainBP: b.again}
		for _, d := range b.drops {
			r.Drops = append(r.Drops, DropWeight{d.code, d.cnt, d.w})
		}
		d.Boxes[id] = r
	}
	return OpsSettings{Version: 1, Drops: d, Rules: []RewardRule{}}
}

func conservativeDropSettings() DropSettings {
	d := defaultOpsSettings().Drops
	for id, r := range d.Recruit {
		if id == 1 || id == 5 {
			r.Weights = map[int]int{3: 790, 4: 200, 5: 10}
			r.Pity = 50
			r.TenMin = 4
			r.RareBP = 30
		}
		if id == 2 {
			r.Weights = map[int]int{2: 350, 3: 500, 4: 148, 5: 2}
			r.TenMin = 4
			r.RareBP = 0
		}
		d.Recruit[id] = r
	}
	r := d.Boxes[10002]
	for i := range r.Drops {
		x := &r.Drops[i]
		if x.Code == "GPFS5" || x.Code == "GPZB5" || x.Code == "ZB1509" || x.Code == "ZB1510" {
			x.Weight = 1
		}
		if x.Code == "GPSP5" || x.Code == "GPJT5" {
			x.Weight = 20
		}
	}
	r.AgainBP = 200
	d.Boxes[10002] = r
	return d
}

func newGMOps(st store.Store, path string) *GMOps {
	o := &GMOps{st: st, path: path, last: map[string]string{}}
	c := defaultOpsSettings()
	if st != nil {
		b, err := st.LoadGlobal(globalGMOps)
		if err == nil {
			err = json.Unmarshal(b, &c)
			if err == nil {
				err = validateOpsSettings(c)
			}
			if err != nil {
				o.startupErr = err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			o.startupErr = err
		}
	}
	o.current.Store(&c)
	return o
}

var opsID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var dropCode = regexp.MustCompile(`^(WP|DZ|ZB|FS|SP|JT|HP)[0-9]+$|^GP(FS|ZB|SP|JT|HP|XBS)[1-5]$`)

func validateOpsSettings(c OpsSettings) error {
	if len(c.Drops.Recruit) != len(recruitPools) || len(c.Drops.Boxes) != len(boxKinds) {
		return errors.New("卡池/宝箱配置不完整")
	}
	for id, r := range c.Drops.Recruit {
		p := recruitPools[id]
		if p == nil || r.Pity < 0 || r.Pity > 99 || r.TenMin < 0 || r.TenMin > 5 || r.RareBP < 0 || r.RareBP > 10000 {
			return fmt.Errorf("卡池%d参数无效", id)
		}
		total := 0
		for star, w := range r.Weights {
			if star < 1 || star > 5 || w < 0 || w > 1000000 || w > 0 && len(poolPets[p.name][star]) == 0 {
				return fmt.Errorf("卡池%d星级权重无效", id)
			}
			total += w
		}
		if total == 0 {
			return errors.New("卡池总权重不能为0")
		}
		if r.Pity > 0 && len(poolPets[p.name][5]) == 0 {
			return errors.New("无五星的卡池不能设置五星保底")
		}
		if r.TenMin > 0 && len(poolPets[p.name][r.TenMin]) == 0 {
			return errors.New("十连保底星级不在卡池中")
		}
	}
	for id, r := range c.Drops.Boxes {
		if boxKinds[id] == nil || len(r.Drops) == 0 || len(r.Drops) > 100 || r.AgainBP < 0 || r.AgainBP > 10000 {
			return errors.New("宝箱配置无效")
		}
		total := 0
		seen := map[string]bool{}
		for _, d := range r.Drops {
			key := fmt.Sprintf("%s/%d", d.Code, d.Count)
			if !validBoxRewardCode(d.Code) || d.Count <= 0 || d.Count > 100000 || d.Weight < 0 || d.Weight > 1000000 || seen[key] {
				return fmt.Errorf("宝箱%d奖励/权重无效: %s", id, d.Code)
			}
			total += d.Weight
			seen[key] = true
		}
		if total == 0 {
			return errors.New("宝箱总权重不能为0")
		}
	}
	if len(c.Rules) > 100 {
		return errors.New("最多100条发奖规则")
	}
	seen := map[string]bool{}
	for _, r := range c.Rules {
		if !opsID.MatchString(r.ID) || seen[r.ID] || strings.TrimSpace(r.Name) == "" {
			return errors.New("规则编号重复或名称无效")
		}
		seen[r.ID] = true
		if r.Mode != "daily" && r.Mode != "eligible" && r.Mode != "once" {
			return errors.New("发奖方式无效")
		}
		if r.Mode != "eligible" {
			if _, err := time.Parse("15:04", r.Time); err != nil {
				return errors.New("发送时间应为 HH:mm")
			}
		}
		for _, v := range []string{r.Date, r.StartDate, r.EndDate} {
			if v != "" {
				if _, err := time.Parse("2006-01-02", v); err != nil {
					return errors.New("日期格式无效")
				}
			}
		}
		if r.Mode == "once" && r.Date == "" {
			return errors.New("单次发送需要日期")
		}
		if r.StartDate != "" && r.EndDate != "" && r.StartDate > r.EndDate {
			return errors.New("结束日期早于开始日期")
		}
		if r.NewPlayerDays < 0 || r.NewPlayerDays > 3650 || r.MinActive < 0 || r.MinActive > 1000 || len(r.UIDs) > 10000 {
			return errors.New("筛选条件无效")
		}
		for _, uid := range r.UIDs {
			if uid <= 0 {
				return errors.New("指定UID必须为正整数，留空表示全服")
			}
		}
		if err := validateOpsMail(r.Reward); err != nil {
			return err
		}
	}
	return nil
}

var opsRewardCatalog = sync.OnceValue(func() map[string]catalogRow {
	out := map[string]catalogRow{}
	for _, r := range gmCatalog() {
		out[r.Code] = r
	}
	return out
})

func validOpsRewardCode(code string) bool {
	if !dropCode.MatchString(code) {
		return false
	}
	if strings.HasPrefix(code, "GP") {
		return true
	}
	return mailItemOK(code)
}

func validateOpsMail(m MailSpec) error {
	if strings.TrimSpace(m.Title) == "" || len(m.Title) > 300 || len(m.Text) > 6000 || m.Gold < 0 || m.Gold > 2000000000 || m.Cash < 0 || m.Cash > 2000000000 || m.Soul < 0 || m.Soul > 2000000000 || len(m.Items) > 100 {
		return errors.New("邮件标题或奖励数值无效")
	}
	for code, n := range m.Items {
		if !mailItemOK(code) || n <= 0 || n > 1000000 {
			return fmt.Errorf("奖励无效: %s", code)
		}
	}
	if len(m.rewards()) == 0 {
		return errors.New("发奖邮件必须有奖励")
	}
	return nil
}

func (o *GMOps) logFile(day string) string {
	if o.path == "" {
		return ""
	}
	ext := filepath.Ext(o.path)
	return strings.TrimSuffix(o.path, ext) + "-" + day + ext
}

func (o *GMOps) audit(e OpsEvent) error {
	o.journalMu.Lock()
	defer o.journalMu.Unlock()
	e.Time = time.Now().Format(time.RFC3339)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if o.path != "" {
		if err = os.MkdirAll(filepath.Dir(o.path), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(o.logFile(today()), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(append(b, '\n'))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	o.recent = append(o.recent, e)
	if len(o.recent) > 500 {
		o.recent = slices.Clone(o.recent[len(o.recent)-500:])
	}
	return nil
}

func (o *GMOps) update(c OpsSettings) error {
	o.runMu.Lock()
	defer o.runMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.startupErr != nil {
		return fmt.Errorf("现有配置读取失败，禁止覆盖: %w", o.startupErr)
	}
	if c.Version != o.current.Load().Version {
		return errors.New("配置已变化，请重新读取后保存")
	}
	if err := validateOpsSettings(c); err != nil {
		return err
	}
	c.Version++
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = o.audit(OpsEvent{Kind: "settings", Status: "requested", Data: c}); err != nil {
		return err
	}
	if o.st != nil {
		if err = o.st.SaveGlobal(globalGMOps, b); err != nil {
			return err
		}
	}
	// Detach all maps/slices owned by the request before publishing.
	var frozen OpsSettings
	if err = json.Unmarshal(b, &frozen); err != nil {
		return err
	}
	o.current.Store(&frozen)
	return o.audit(OpsEvent{Kind: "settings", Status: "saved", Data: map[string]any{"version": c.Version}})
}
