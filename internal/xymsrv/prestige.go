package xymsrv

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"turnrpg/internal/xym"
)

// Existing level/gate points are retained by owner instruction. VIP and Lundao
// tiers come from client auto/achieve.lua 11101..11115 / 10201..10219;
// AchieveView sums all attained tiers, rather than taking just the last tier.
var vipPrestige = [...]int{0, 3, 6, 9, 12, 15, 18, 21, 24, 27, 30, 33, 36, 39, 42, 45}
var lundaoPrestigeRanks = [...]int{8800, 7600, 6500, 5500, 4500, 3600, 2800, 2100, 1500, 1000, 600, 300, 100, 50, 25, 10, 5, 1}

type PrestigeBand struct {
	Through int `json:"through"` // inclusive current rank; one matching band, not cumulative
	Points  int `json:"points"`
}

// Empty bands mean no extra reward until the owner confirms the numbers.
type PrestigeRules struct {
	Qujing  []PrestigeBand `json:"qujing"`
	Stars   []PrestigeBand `json:"stars"`
	Shilian []PrestigeBand `json:"shilian"`
	Tianti  []PrestigeBand `json:"tianti"`
}

func LoadPrestigeRules(path string) (PrestigeRules, error) {
	var rules PrestigeRules
	if path == "" {
		return rules, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return rules, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&rules); err != nil {
		return rules, err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return rules, fmt.Errorf("unexpected content after prestige rules")
	}
	for name, bands := range map[string][]PrestigeBand{"qujing": rules.Qujing, "stars": rules.Stars, "shilian": rules.Shilian, "tianti": rules.Tianti} {
		lastRank, lastPoints := 0, 1_000_000
		for _, b := range bands {
			if b.Through <= lastRank || b.Points < 0 || b.Points > lastPoints || b.Points > 1_000_000 {
				return rules, fmt.Errorf("invalid prestige bands: %s", name)
			}
			lastRank, lastPoints = b.Through, b.Points
		}
	}
	return rules, nil
}

func prestigeAtRank(bands []PrestigeBand, rank int) int {
	if rank > 0 {
		for _, b := range bands {
			if rank <= b.Through {
				return b.Points
			}
		}
	}
	return 0
}

func prestigeOf(ro *Role, p *Player) int {
	if ro == nil {
		return 0
	}
	n := max(ro.Grade, 0) * 10
	for level := 1; level <= min(ro.VIP, len(vipPrestige)-1); level++ {
		n += vipPrestige[level]
	}
	if p != nil {
		for _, pr := range p.Prog {
			if pr == nil {
				continue
			}
			for _, count := range pr.Gates {
				if count > 0 {
					n++
				}
			}
		}
	}
	return n
}

func (srv *Server) prestigeOf(ro *Role, p *Player) int {
	n := prestigeOf(ro, p)
	if ro == nil {
		return n
	}
	var rank int
	srv.ladder.do(func() bool { rank = srv.ladder.rankOf(ro.UID, ro.Grade); return false })
	if rank > 0 {
		n++ // 10201: started Lundao
		for i, through := range lundaoPrestigeRanks {
			if rank <= through {
				n += i + 2
			}
		}
	}
	rules := srv.cfg.PrestigeRules
	if len(rules.Qujing) > 0 {
		for i, r := range srv.qjRankList() {
			if r.UID == ro.UID {
				n += prestigeAtRank(rules.Qujing, i+1)
				break
			}
		}
	}
	if len(rules.Stars) > 0 && p != nil {
		srv.lrEntries(p)
		srv.lrCache.Lock()
		var rows []lrEntry
		for _, r := range srv.lrCache.list {
			if r.Star > 0 {
				rows = append(rows, *r)
			}
		}
		srv.lrCache.Unlock()
		slices.SortStableFunc(rows, func(a, b lrEntry) int {
			if a.Star != b.Star {
				return b.Star - a.Star
			}
			return b.FP - a.FP
		})
		for i, r := range rows[:min(len(rows), lrTop)] {
			if r.UID == ro.UID {
				n += prestigeAtRank(rules.Stars, i+1)
				break
			}
		}
	}
	if len(rules.Shilian) > 0 {
		for i, r := range slRank.list(slGroup(ro.Grade)) {
			if r.UID == ro.UID {
				n += prestigeAtRank(rules.Shilian, i+1)
				break
			}
		}
	}
	if len(rules.Tianti) > 0 {
		srv.tianti.do(func() bool {
			n += prestigeAtRank(rules.Tianti, srv.tianti.rankOf(ro.UID, ttGroup(ro.Grade)))
			return false
		})
	}
	return n
}

func init() {
	Handle("ACHIEVE_PTO.C_GET_ALL_ACH", func(s *Session, p *Player, _ xym.Msg) {
		rows := []xym.Msg{}
		if vip := min(s.role.VIP, 15); vip > 0 {
			rows = append(rows, xym.Msg{"aid": 11100 + vip, "curr": vip})
		}
		var rank int
		s.srv.ladder.do(func() bool { rank = s.srv.ladder.rankOf(p.UID, s.role.Grade); return false })
		if rank > 0 {
			level := 1
			for i, through := range lundaoPrestigeRanks {
				if rank <= through {
					level = i + 2
				}
			}
			rows = append(rows, xym.Msg{"aid": 10200 + level, "curr": rank})
		}
		s.SendExt("ACHIEVE_PTO.S_ACH_OPEN", xym.Msg{"wei": s.prestige(), "achieve": rows})
	})
}

// Kept here so config diagnostics never need to print file contents or secrets.
func (r PrestigeRules) String() string {
	var enabled []string
	for name, bands := range map[string][]PrestigeBand{"qujing": r.Qujing, "stars": r.Stars, "shilian": r.Shilian, "tianti": r.Tianti} {
		if len(bands) > 0 {
			enabled = append(enabled, name)
		}
	}
	slices.Sort(enabled)
	return strings.Join(enabled, ",")
}
