package xymsrv

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"turnrpg/internal/xym"
)

// LoadHotfix 读 dir（可用逗号分隔多个目录，按顺序拼接）下的 *.lua，每个目录内按文件名排序，编成一条 S_SEND_NEW_HOTFIX_CONTENT（官方服在
// S_SEND_SID 之后、S_LOGIN_SUC 之前发）。文件名形如 "01_1.5.403.lua"：下划线后面是 hotfix_id，
// 没有下划线就用 defID。dir 为空返回 nil（不发）。
func LoadHotfix(dir, defID string) ([]byte, int, error) {
	if dir == "" {
		return nil, 0, nil
	}
	var files []string
	for _, d := range strings.Split(dir, ",") {
		l, err := filepath.Glob(filepath.Join(strings.TrimSpace(d), "*.lua"))
		if err != nil {
			return nil, 0, err
		}
		sort.Strings(l)
		files = append(files, l...)
	}
	var contents []xym.Msg
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, 0, err
		}
		id := defID
		if _, after, ok := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".lua"), "_"); ok {
			id = after
		}
		contents = append(contents, xym.Msg{"hotfix_id": id, "hotfix_content": minifyLua(data)})
	}
	body, err := xym.ByName("LOGIN_PTO.S_SEND_NEW_HOTFIX_CONTENT").Encode(xym.Msg{"contents": contents})
	if err != nil {
		return nil, 0, err
	}
	if len(body) > 0xffff {
		return nil, 0, os.ErrInvalid // 一帧装不下（s2c 长度字段是 u16）
	}
	return body, len(contents), nil
}

// minifyLua 去掉 Lua 源码里的整行注释、行首缩进和空行（代码不变，只为省字节：所有补丁合在一条消息里，
// 帧长度是 u16，超过 64KB 服务器起不来——2026-10-22 加补丁 07 时出过一次）。含 [[ / [=[ 的文件（多行字符串、
// 块注释）原样返回，避免改到字符串内容。
func minifyLua(src []byte) []byte {
	s := string(src)
	if strings.Contains(s, "[[") || strings.Contains(s, "[=[") {
		return src
	}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimLeft(line, " \t")
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		out = append(out, t)
	}
	return []byte(strings.Join(out, "\n") + "\n")
}
