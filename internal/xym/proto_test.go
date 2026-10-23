package xym

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTableLoaded(t *testing.T) {
	if n := len(All(C2S)); n != 890 {
		t.Errorf("C2S messages = %d, want 890", n)
	}
	// 545 条 S_ 消息里有 4 对共用协议号，所以按协议号只有 541 个
	if n := len(All(S2C)); n != 541 || len(Duplicates) != 4 {
		t.Errorf("S2C pids = %d (want 541), duplicates = %d (want 4)", n, len(Duplicates))
	}
	s := ByName("LOGIN_PTO.S_SEND_SID")
	if s == nil || s.PID != 2002 {
		t.Fatalf("LOGIN_PTO.S_SEND_SID = %+v", s)
	}
}

func TestRoundTripSynthetic(t *testing.T) {
	s := ByName("ACHIEVE_PTO.S_ACH_OPEN") // int32 wei + array{int16 aid, int32 curr}
	in := Msg{"wei": int64(-5), "achieve": []Msg{{"aid": int64(7), "curr": int64(123456)}, {"aid": int64(65535), "curr": int64(-1)}}}
	b := s.MustEncode(in)
	_, got, m, err := DecodeExt(S2C, b)
	if err != nil || got != s {
		t.Fatalf("decode: %v %v", got, err)
	}
	b2 := s.MustEncode(m)
	if !bytes.Equal(b, b2) {
		t.Fatalf("re-encode mismatch\n%x\n%x", b, b2)
	}
	if m["wei"].(int64) != -5 || m["achieve"].([]Msg)[1]["aid"].(int64) != 65535 {
		t.Fatalf("values: %v", m)
	}
}

func TestFrames(t *testing.T) {
	var buf []byte
	buf = AppendC2S(buf, MainExt, []byte{1, 2, 3})
	buf = AppendC2S(buf, MainExtLarge, bytes.Repeat([]byte{9}, 300))
	r := bufio.NewReader(bytes.NewReader(buf))
	for _, want := range []struct {
		main byte
		n    int
	}{{MainExt, 3}, {MainExtLarge, 300}} {
		m, b, err := ReadC2S(r)
		if err != nil || m != want.main || len(b) != want.n {
			t.Fatalf("got main=%d len=%d err=%v", m, len(b), err)
		}
	}
	s2c := AppendS2C(nil, SSeed, U32LE(0))
	if hex.EncodeToString(s2c) != "03040000000000" { // 与 47.96.252.180:28417 实测的第一帧相同
		t.Fatalf("seed frame %x", s2c)
	}
}

// TestCaptureRoundTrip 用抓包验证：每个带 raw 的扩展协议包，解码后重新编码必须逐字节相同。
// XYM_CAPTURES 指向含 *.jsonl 的目录（analysis/captures）；未设置则跳过。
func TestCaptureRoundTrip(t *testing.T) {
	dir := os.Getenv("XYM_CAPTURES")
	if dir == "" {
		t.Skip("XYM_CAPTURES not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	n, bad := 0, 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			var r struct {
				Dir  string `json:"dir"`
				Main int    `json:"main"`
				Raw  string `json:"raw"`
			}
			if len(line) == 0 || json.Unmarshal(line, &r) != nil || r.Raw == "" || (r.Main != 1 && r.Main != 254) {
				continue
			}
			body, _ := hex.DecodeString(r.Raw)
			d := S2C
			if r.Dir == "C2S" {
				d = C2S
			}
			pid, s, m, err := DecodeExt(d, body)
			n++
			if err != nil {
				bad++
				if bad <= 10 {
					t.Errorf("%s pid %d: %v", r.Dir, pid, err)
				}
				continue
			}
			b2, err := s.Encode(m)
			if err != nil || !bytes.Equal(body, b2) {
				bad++
				if bad <= 10 {
					t.Errorf("%s %s.%s: re-encode mismatch (%v)", r.Dir, s.Module, s.Name, err)
				}
			}
		}
	}
	t.Logf("checked %d ext frames from %d files, %d bad", n, len(files), bad)
	if n == 0 {
		t.Fatal("no frames checked")
	}
}
