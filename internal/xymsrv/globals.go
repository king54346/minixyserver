package xymsrv

import (
	"encoding/json"
	"errors"
	"log/slog"

	"turnrpg/internal/store"
)

// 全服数据（好友 / 邮件、洞府、论道榜、星战、天梯、九幽排行）各存成一条「玩家存档」，占用保留的 uid
// （真实角色从 20001 起，见 accounts.go）。新加全服数据时在这里登记，别撞号。
const (
	socialUID  int32 = 0 // social.go
	dongfuUID  int32 = 1 // dongfu.go
	lundaoUID  int32 = 2 // lundao.go
	starwarUID int32 = 3 // starwar.go
	tiantiUID  int32 = 4 // tianti.go
	jiuyouUID  int32 = 5 // jiuyou.go
	treeUID    int32 = 6 // tree.go 摇钱树林
)

// loadGlobal 把保留 uid 的存档读进 v（没有存档时 v 不变；读坏了只记日志，按新数据启动）。
func loadGlobal(st store.Store, uid int32, v any, name string) {
	if st == nil {
		return
	}
	data, err := st.LoadPlayer(uid)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, v); err != nil {
			slog.Error("load "+name, "err", err)
		}
	case !errors.Is(err, store.ErrNotFound):
		slog.Error("load "+name, "err", err)
	}
}

// saveGlobal 把 v 写回保留 uid（st 为 nil 时什么都不做，测试里不落盘）。调用方持有 v 的锁。
func saveGlobal(st store.Store, uid int32, v any, name string) {
	if st == nil {
		return
	}
	data, err := json.Marshal(v)
	if err == nil {
		err = st.SavePlayer(uid, data)
	}
	if err != nil {
		slog.Error("save "+name, "err", err)
	}
}
