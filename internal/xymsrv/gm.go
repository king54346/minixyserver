package xymsrv

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"turnrpg/internal/gamedata"
)

// GM 工具（Desktop/mnxy/GM，本地网页 → SSH → 本机管理接口）用的管理接口：
//
//	GET  /catalog                 可发放物品目录 [{code, name, kind, star}]（WP 道具 / DZ 护法卡 / ZB 装备 / FS 法术书）
//	GET  /player?uid=20005        角色详情：角色数值 + 背包概况
//	POST /role {"uid": 20005, "set": {"gold": 1000, "grade": 100, …}}
//	            改角色数值（gold cash soulexp friendpoint grade exp vip tpoint hpoint）；在线的先踢下线；返回改后的角色
//	POST /kick {"uid": 20005}     踢下线
//
// 发放物品走 POST /mail（admin.go）。

type catalogRow struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Star int    `json:"star,omitempty"`
}

func gmCatalog() []catalogRow {
	var out []catalogRow
	for id, it := range gamedata.Items {
		if it != nil && it.Name != "" {
			out = append(out, catalogRow{"WP" + strconv.Itoa(id), it.Name, "道具", 0})
		}
	}
	for id, pi := range gamedata.Pets {
		if pi != nil && pi.Name != "" {
			out = append(out, catalogRow{"DZ" + strconv.Itoa(int(id)), pi.Name, "护法", pi.Star})
		}
	}
	for id, si := range gamedata.Suits {
		if si != nil && si.Name != "" {
			out = append(out, catalogRow{"ZB" + strconv.Itoa(id), si.Name, "装备", si.Star})
		}
	}
	for id, mi := range gamedata.Magics {
		if mi != nil && mi.Name != "" && id >= 11000 && id < 15000 {
			out = append(out, catalogRow{"FS" + strconv.Itoa(int(id)), mi.Name, "法术书", mi.Star})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// kickWait 把在线的 uid 踢下线并等会话结束（存档在会话结束时写完），最多 5 秒。
func (srv *Server) kickWait(uid int32) error {
	s := srv.sessionOf(uid)
	if s == nil {
		return nil
	}
	s.Close()
	for range 50 {
		if srv.sessionOf(uid) == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("role still online")
}

func (srv *Server) gmPlayer(uid int32) (map[string]any, error) {
	ro := srv.roles.Get(uid)
	if ro == nil {
		return nil, errors.New("no such role")
	}
	out := map[string]any{"role": ro, "online": srv.sessionOf(uid) != nil}
	if p, err := srv.players.Get(uid); err == nil {
		p.mu.Lock()
		out["pets"], out["suits"], out["books"], out["items"] = len(p.Pets), len(p.Suits), len(p.Books), len(p.Items)
		out["bag_limit"] = p.BagLimit
		p.mu.Unlock()
	}
	return out, nil
}

// gmSetRole 改角色数值。可改的键见 gmRoleKeys；数值不能为负，等级不超过满级。
func (srv *Server) gmSetRole(uid int32, set map[string]int64) (*Role, error) {
	if srv.roles.Get(uid) == nil {
		return nil, errors.New("no such role")
	}
	for k, v := range set {
		if _, ok := gmRoleKeys[k]; !ok {
			return nil, errors.New("unknown field " + k)
		}
		if v < 0 || v > 2_000_000_000 {
			return nil, errors.New("bad value for " + k)
		}
	}
	if err := srv.kickWait(uid); err != nil {
		return nil, err
	}
	return srv.roles.Update(uid, func(ro *Role) bool {
		for k, v := range set {
			gmRoleKeys[k](ro, v)
		}
		return true
	})
}

var gmRoleKeys = map[string]func(ro *Role, v int64){
	"gold":        func(ro *Role, v int64) { ro.Gold = int32(v) },
	"cash":        func(ro *Role, v int64) { ro.Cash = int32(v) },
	"soulexp":     func(ro *Role, v int64) { ro.SoulExp = int32(v) },
	"friendpoint": func(ro *Role, v int64) { ro.FriendPoint = int32(v) },
	"grade":       func(ro *Role, v int64) { ro.Grade = int(min(max(v, 1), int64(gamedata.MaxRoleGrade))) },
	"exp":         func(ro *Role, v int64) { ro.Exp = int32(v) },
	"vip":         func(ro *Role, v int64) { ro.VIP = int(min(v, 15)) },
	"tpoint":      func(ro *Role, v int64) { ro.TPoint = int(min(v, 9999)) },
	"hpoint":      func(ro *Role, v int64) { ro.HPoint = int(min(v, 9999)) },
}

func (srv *Server) gmHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, gmCatalog())
	})
	mux.HandleFunc("GET /player", func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		out, err := srv.gmPlayer(int32(uid))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /role", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32            `json:"uid"`
			Set map[string]int64 `json:"set"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ro, err := srv.gmSetRole(req.UID, req.Set)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, ro)
	})
	mux.HandleFunc("POST /kick", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32 `json:"uid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := srv.kickWait(req.UID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "kicked"})
	})
}
