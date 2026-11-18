package xymsrv

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMinifyLua：去掉整行注释、行首缩进和空行，代码行保留；含 [[ 的文件原样不动。
func TestMinifyLua(t *testing.T) {
	src := "-- 说明\r\nlocal a = 1\n\n  -- 缩进的注释\n  if a then\n    print(\"--不是注释\") -- 行尾注释保留\n  end\n"
	want := "local a = 1\nif a then\nprint(\"--不是注释\") -- 行尾注释保留\nend\n"
	if got := string(minifyLua([]byte(src))); got != want {
		t.Fatalf("got %q", got)
	}
	raw := "local s = [[\n  -- 字符串里的\n]]\n"
	if got := string(minifyLua([]byte(raw))); got != raw {
		t.Fatalf("multi-line string changed: %q", got)
	}
}

// luaStrings 解出源码里所有引号字符串的字节值（测试用的简化版：只认 \ddd 和单字符转义）。
func luaStrings(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' && i+1 < len(s) && s[i+1] == '-' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if c != '"' && c != '\'' {
			continue
		}
		var b []byte
		for i++; i < len(s) && s[i] != c; i++ {
			if s[i] != 92 {
				b = append(b, s[i])
				continue
			}
			i++
			if isDigit(s[i]) {
				n, k := 0, 0
				for ; k < 3 && isDigit(s[i+k]); k++ {
					n = n*10 + int(s[i+k]-'0')
				}
				b = append(b, byte(n))
				i += k - 1
			} else {
				b = append(b, 92, s[i])
			}
		}
		out = append(out, string(b))
	}
	return out
}

// TestShortEscapes：缩短 \ddd 后，所有补丁里的字符串字节不变，而且确实变小了。
func TestShortEscapes(t *testing.T) {
	if got := shortEscapes(`a = "\000\0011\065" -- "\000"` + "\n"); got != `a = "\0\0011\65" -- "\000"`+"\n" {
		t.Fatalf("got %q", got)
	}
	files, _ := filepath.Glob("../../../analysis/data/hotfix/*.lua")
	more, _ := filepath.Glob("../../hotfix-local/*.lua")
	before, after := 0, 0
	for _, f := range append(files, more...) {
		src, _ := os.ReadFile(f)
		s := string(src)
		if strings.Contains(s, "[[") {
			continue
		}
		short := shortEscapes(s)
		before, after = before+len(s), after+len(short)
		if !slices.Equal(luaStrings(s), luaStrings(short)) {
			t.Fatalf("%s: 字符串内容变了", f)
		}
	}
	if len(files) > 0 && after >= before {
		t.Fatalf("没有变小: %d → %d", before, after)
	}
	t.Logf("%d → %d", before, after)
}
