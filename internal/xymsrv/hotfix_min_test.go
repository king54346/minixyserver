package xymsrv

import "testing"

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
