package xymsrv

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"turnrpg/internal/xym"
)

// LoadHotfix 读 dir（可用逗号分隔多个目录，按顺序拼接）下的 *.lua，每个目录内按文件名排序，编成一条 S_SEND_NEW_HOTFIX_CONTENT（官方服在
// S_SEND_SID 之后、S_LOGIN_SUC 之前发）。文件名形如 "01_1.5.403.lua"：下划线后面是 hotfix_id，
// 没有下划线就用 defID。dir 为空返回 nil（不发）。
func LoadHotfix(dir, defID string) ([]byte, int, error) {
	batches, n, err := LoadHotfixBatches(dir, defID)
	if err != nil || len(batches) == 0 {
		return nil, n, err
	}
	if len(batches) > 1 {
		return nil, 0, os.ErrInvalid
	}
	return batches[0], n, nil
}

// LoadHotfixBatches keeps each Lua file intact and in order, splitting between
// files when the uint16 frame limit is reached. Login sends all batches before
// S_LOGIN_SUC so dependent patches are installed before player data is handled.
func LoadHotfixBatches(dir, defID string) ([][]byte, int, error) {
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
	var batches [][]byte
	var contents []xym.Msg
	var body []byte
	count := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, 0, err
		}
		id := defID
		if _, after, ok := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".lua"), "_"); ok {
			id = after
		}
		code := minifyLua(data)
		for _, version := range compatibleHotfixVersions(data, id) {
			entry := xym.Msg{"hotfix_id": version, "hotfix_content": code}
			candidate := append(contents, entry)
			encoded, err := xym.ByName("LOGIN_PTO.S_SEND_NEW_HOTFIX_CONTENT").Encode(xym.Msg{"contents": candidate})
			if err != nil {
				return nil, 0, err
			}
			if len(encoded) > 0xffff {
				if len(contents) == 0 {
					return nil, 0, os.ErrInvalid
				}
				batches = append(batches, body)
				candidate = []xym.Msg{entry}
				encoded, err = xym.ByName("LOGIN_PTO.S_SEND_NEW_HOTFIX_CONTENT").Encode(xym.Msg{"contents": candidate})
				if err != nil || len(encoded) > 0xffff {
					return nil, 0, os.ErrInvalid
				}
			}
			contents, body = candidate, encoded
			count++
		}
	}
	if len(body) > 0 {
		batches = append(batches, body)
	}
	return batches, count, nil
}

// Version matching is performed against res.plist by the stock Lua loader,
// independently of the login header or Android versionName. Only explicitly
// opted-in local patches may run on an additional resource version.
func compatibleHotfixVersions(src []byte, original string) []string {
	versions := []string{original}
	seen := map[string]bool{original: true}
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-- @compat ") {
			continue
		}
		for _, version := range strings.Fields(strings.TrimPrefix(line, "-- @compat ")) {
			parts := strings.Split(version, ".")
			valid := len(parts) == 3
			for _, part := range parts {
				n, err := strconv.Atoi(part)
				valid = valid && err == nil && n >= 0 && n <= 65535
			}
			if valid && !seen[version] {
				versions = append(versions, version)
				seen[version] = true
			}
		}
	}
	return versions
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
	return []byte(shortEscapes(strings.Join(out, "\n") + "\n"))
}

// shortEscapes 把引号字符串里的十进制转义 \ddd 写成最短形式（\000 → \0），后面紧跟数字时保持原样以免连读。
// 补丁 08（离线补的界面模块，整段字节码写成 \ddd）因此小了约 7KB。只改 "…" / '…' 里的转义，注释和字符串外照抄。
func shortEscapes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == 0 {
			if c == '-' && i+1 < len(s) && s[i+1] == '-' { // 行尾注释照抄（里面的引号不算）
				j := strings.IndexByte(s[i:], '\n')
				if j < 0 {
					j = len(s) - i
				}
				b.WriteString(s[i : i+j])
				i += j - 1
				continue
			}
			if c == '"' || c == '\'' {
				quote = c
			}
			b.WriteByte(c)
			continue
		}
		switch {
		case c == quote:
			quote = 0
			b.WriteByte(c)
		case c == '\\' && i+1 < len(s) && isDigit(s[i+1]):
			j := i + 1
			for j < len(s) && j < i+4 && isDigit(s[j]) {
				j++
			}
			if j < len(s) && isDigit(s[j]) {
				b.WriteString(s[i:j]) // 后面还是数字：保留原写法
			} else {
				n, _ := strconv.Atoi(s[i+1 : j])
				b.WriteByte('\\')
				b.WriteString(strconv.Itoa(n))
			}
			i = j - 1
		case c == '\\' && i+1 < len(s):
			b.WriteString(s[i : i+2])
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
