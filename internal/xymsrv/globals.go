package xymsrv

import (
	"encoding/json"
	"errors"
	"log/slog"

	"turnrpg/internal/store"
)

// 全服数据（好友 / 邮件、洞府、论道榜、星战、天梯、九幽排行…）各存成存档库 globals 表的一行（按名字，见 internal/model）。
// 新加全服数据时在这里登记一个名字，别重名。
type globalKey string

const (
	globalSocial  globalKey = "social"  // social.go 好友 / 邮件
	globalDongfu  globalKey = "dongfu"  // dongfu.go
	globalLundao  globalKey = "lundao"  // lundao.go
	globalStarwar globalKey = "starwar" // starwar.go
	globalTianti  globalKey = "tianti"  // tianti.go
	globalJiuyou  globalKey = "jiuyou"  // jiuyou.go
	globalTree    globalKey = "tree"    // tree.go 摇钱树林
	globalLiexi   globalKey = "liexi"   // liexi_jjc.go 时空竞技场积分榜
	globalQujing  globalKey = "qujing"  // qujing.go 西天取经进度榜
	globalChat    globalKey = "chat"    // h_chat.go 聊天记录（世界 / 各洞府频道各留最近 50 条，重启不清）
	globalDraft   globalKey = "weekly_draft"
)

// loadGlobal 把全服数据 key 读进 v（没有存档时 v 不变；读坏了只记日志，按新数据启动）。
func loadGlobal(st store.Store, key globalKey, v any) {
	if st == nil {
		return
	}
	data, err := st.LoadGlobal(string(key))
	switch {
	case err == nil:
		if err := json.Unmarshal(data, v); err != nil {
			slog.Error("load global", "name", key, "err", err)
		}
	case !errors.Is(err, store.ErrNotFound):
		slog.Error("load global", "name", key, "err", err)
	}
}

// saveGlobal 把 v 写回全服数据 key（st 为 nil 时什么都不做，测试里不落盘）。调用方持有 v 的锁。
func saveGlobal(st store.Store, key globalKey, v any) {
	if st == nil {
		return
	}
	data, err := json.Marshal(v)
	if err == nil {
		err = st.SaveGlobal(string(key), data)
	}
	if err != nil {
		slog.Error("save global", "name", key, "err", err)
	}
}
