package xymsrv

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"turnrpg/internal/battle"
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func (srv *Server) gmRecord(file string) (xym.Msg, []battle.Cmd, error) {
	body, err := srv.readRecord(file)
	if err != nil {
		return nil, nil, err
	}
	_, spec, msg, err := xym.DecodeExt(xym.S2C, body)
	if err != nil || spec == nil || spec.Name != "S_FIGHT_RECORD" {
		return nil, nil, fmt.Errorf("战报格式无效")
	}
	raw, _ := msg["record"].([]byte)
	cmds, err := battle.DecodeRecord(raw)
	if err != nil {
		return nil, nil, err
	}
	return msg, cmds, nil
}

func (srv *Server) gmRecordHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /records/player", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		uid, err := strconv.ParseInt(q.Get("uid"), 10, 32)
		day := q.Get("day")
		if day == "" {
			day = time.Now().Format("20060102")
		}
		_, dateErr := time.Parse("20060102", day)
		value := q.Get("offset")
		if value == "" {
			value = "0"
		}
		offset, pageErr := strconv.Atoi(value)
		if err != nil || uid <= 0 || dateErr != nil || pageErr != nil || offset < 0 {
			writeJSON(w, 400, xym.Msg{"error": "角色、日期或分页参数无效"})
			return
		}
		files := srv.listRecords(day, int32(uid))
		start := min(offset, len(files))
		end := min(start+30, len(files))
		rows := make([]xym.Msg, 0, end-start)
		for _, file := range files[start:end] {
			row := xym.Msg{"file": file, "time": filepath.Base(file)[:6]}
			msg, cmds, err := srv.gmRecord(file)
			if err != nil {
				row["error"] = "战报暂不可读"
			} else {
				row["rid"], row["opponent"] = msg["rid"], msg["name2"]
				for i := len(cmds) - 1; i >= 0; i-- {
					if cmds[i].ID == battle.CmdWarEnd {
						row["result"] = map[bool]string{true: "胜利", false: "未胜"}[cmds[i].Magic == 1]
						break
					}
				}
			}
			rows = append(rows, row)
		}
		writeJSON(w, 200, xym.Msg{"rows": rows, "total": len(files), "day": day})
	})
	mux.HandleFunc("GET /records/detail", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		uid, err := strconv.ParseInt(q.Get("uid"), 10, 32)
		file := q.Get("f")
		if err != nil || uid <= 0 || recordOwner(file) != int32(uid) {
			writeJSON(w, 400, xym.Msg{"error": "战报与角色不匹配"})
			return
		}
		msg, cmds, err := srv.gmRecord(file)
		if err != nil {
			writeJSON(w, 404, xym.Msg{"error": "战报不存在或无法解码"})
			return
		}
		for _, side := range []string{"1", "2"} {
			team, _ := msg["team"+side].([]xym.Msg)
			for _, pet := range team {
				if icon, ok := pet["icon"].(int64); ok {
					if info := gamedata.Pets[int32(icon)]; info != nil {
						pet["name"] = info.Name
					}
				}
			}
		}
		delete(msg, "record")
		const maxCommands = 3000
		rows := make([]xym.Msg, 0, min(len(cmds), maxCommands))
		for _, cmd := range cmds[:min(len(cmds), maxCommands)] {
			name := ""
			id := int32(cmd.Magic)
			if info := gamedata.Talents[id]; info != nil {
				name = info.Family
			} else if info := gamedata.Magics[id]; info != nil {
				name = info.Name
			} else if info := gamedata.Buffs[id]; info != nil {
				name = info.Name
			}
			rows = append(rows, xym.Msg{"id": cmd.ID, "actor": cmd.Actor, "magic": cmd.Magic, "name": name, "targets": cmd.Targets})
		}
		writeJSON(w, 200, xym.Msg{"fight": msg, "commands": rows, "total_commands": len(cmds)})
	})
}
