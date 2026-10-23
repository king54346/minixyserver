// Package xym 实现迷你西游（细水长流私服）客户端的 TCP 协议。
//
// 依据（client/lua/decompiled/script/）：net.lua（分帧、主命令）、nex_prot.lua（扩展协议编解码）、
// auto/proto.lua（全部 53 个模块、1435 条消息的字段模板，导出为内嵌的 proto.json）。
//
// 扩展协议 body = [CMD u16][SUB u8][字段...]，协议号 PID = CMD*1000+SUB。字段（小端）：
//
//	int8 / int16  读作无符号（nex_prot.pop_fixint 用 "<I"），写作有符号（push_fixint 用 "<i"）
//	int32         有符号
//	varint        LEB128（proto.json 里实际没有用到，保留实现）
//	varstr/buffer u16 长度 + 字节
//	array         u16 个数 + 子结构
package xym

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed proto.json
var protoJSON []byte

// Field 是字段模板的一项：{类型, 名字, 子结构(仅 array)}。
type Field struct {
	Type string
	Name string
	Sub  []Field
}

// Spec 是一条消息的定义。
type Spec struct {
	Module string
	Name   string // C_xxx / S_xxx
	PID    int
	Desc   string
	Lock   string // 服务端消息的 LOCK：客户端收到后解除同名等待锁
	Body   []Field
}

// Dir 区分消息方向。
type Dir int

const (
	C2S Dir = iota // 客户端发
	S2C            // 服务端发
)

// Duplicates 记录同方向重复的协议号（见 init）。
var Duplicates [][2]string

var (
	byPID  = [2]map[int]*Spec{{}, {}}
	byName = map[string]*Spec{} // "MODULE.NAME"
)

func init() {
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(protoJSON, &raw); err != nil {
		panic("xym: proto.json: " + err.Error())
	}
	for mod, entries := range raw {
		for name, msg := range entries {
			var d Dir
			switch {
			case strings.HasPrefix(name, "C_"):
				d = C2S
			case strings.HasPrefix(name, "S_"):
				d = S2C
			default:
				continue
			}
			var m struct {
				SUBCMD int
				DESC   string
				LOCK   json.RawMessage
				BODY   []json.RawMessage
			}
			if err := json.Unmarshal(msg, &m); err != nil {
				panic(fmt.Sprintf("xym: %s.%s: %v", mod, name, err))
			}
			body, err := parseFields(m.BODY)
			if err != nil {
				panic(fmt.Sprintf("xym: %s.%s: %v", mod, name, err))
			}
			s := &Spec{Module: mod, Name: name, PID: m.SUBCMD, Desc: m.DESC, Body: body}
			if len(m.LOCK) > 0 {
				_ = json.Unmarshal(m.LOCK, &s.Lock)
			}
			// 原表有 4 个协议号各对应两条 S_ 消息（25015/25021/9010/6020）；客户端按 pairs 顺序
			// 建表，胜出者不确定。这里固定取 "MODULE.NAME" 字典序小的那条，并记进 Duplicates。
			if old := byPID[d][s.PID]; old != nil {
				Duplicates = append(Duplicates, [2]string{old.Module + "." + old.Name, mod + "." + name})
				if old.Module+"."+old.Name < mod+"."+name {
					byName[mod+"."+name] = s
					continue
				}
			}
			byPID[d][s.PID] = s
			byName[mod+"."+name] = s
		}
	}
}

func parseFields(items []json.RawMessage) ([]Field, error) {
	out := make([]Field, 0, len(items))
	for _, it := range items {
		var parts []json.RawMessage
		if err := json.Unmarshal(it, &parts); err != nil || len(parts) < 2 {
			return nil, fmt.Errorf("bad field %s", it)
		}
		var f Field
		_ = json.Unmarshal(parts[0], &f.Type)
		_ = json.Unmarshal(parts[1], &f.Name)
		if f.Type == "array" {
			var sub []json.RawMessage
			if len(parts) < 3 || json.Unmarshal(parts[2], &sub) != nil {
				return nil, fmt.Errorf("array %s without sub fields", f.Name)
			}
			var err error
			if f.Sub, err = parseFields(sub); err != nil {
				return nil, err
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// Lookup 按方向和协议号找消息定义。
func Lookup(d Dir, pid int) *Spec { return byPID[d][pid] }

// ByName 按 "MODULE.NAME" 找消息定义，例如 "LOGIN_PTO.S_SEND_SID"。
func ByName(full string) *Spec { return byName[full] }

// All 返回某个方向的全部消息，按协议号排序。
func All(d Dir) []*Spec {
	out := make([]*Spec, 0, len(byPID[d]))
	for _, s := range byPID[d] {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// Msg 是解码后的字段：int 字段为 int64，varstr 为 string，buffer 为 []byte，array 为 []Msg。
type Msg = map[string]any

// Encode 把字段按 spec 编成扩展协议 body（含 CMD/SUB 头）。缺失的字段按零值编码。
func (s *Spec) Encode(m Msg) ([]byte, error) {
	out := binary.LittleEndian.AppendUint16(nil, uint16(s.PID/1000))
	out = append(out, byte(s.PID%1000))
	return encodeFields(out, s.Body, m)
}

// MustEncode 同 Encode，出错 panic（只用于服务端自己构造的、字段确定的消息）。
func (s *Spec) MustEncode(m Msg) []byte {
	b, err := s.Encode(m)
	if err != nil {
		panic(fmt.Sprintf("xym: encode %s.%s: %v", s.Module, s.Name, err))
	}
	return b
}

func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case nil:
		return 0, nil
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case int32:
		return int64(x), nil
	case float64:
		return int64(x), nil
	case json.Number:
		return x.Int64()
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("not a number: %T", v)
}

func encodeFields(out []byte, tmpl []Field, m Msg) ([]byte, error) {
	for _, f := range tmpl {
		v := m[f.Name]
		switch f.Type {
		case "varstr", "buffer":
			var b []byte
			switch x := v.(type) {
			case nil:
			case string:
				b = []byte(x)
			case []byte:
				b = x
			default:
				return nil, fmt.Errorf("%s: want string, got %T", f.Name, v)
			}
			out = binary.LittleEndian.AppendUint16(out, uint16(len(b)))
			out = append(out, b...)
		case "array":
			var items []Msg
			switch x := v.(type) {
			case nil:
			case []Msg:
				items = x
			case []any:
				for _, it := range x {
					mm, ok := it.(Msg)
					if !ok {
						return nil, fmt.Errorf("%s: array item %T", f.Name, it)
					}
					items = append(items, mm)
				}
			default:
				return nil, fmt.Errorf("%s: want array, got %T", f.Name, v)
			}
			out = binary.LittleEndian.AppendUint16(out, uint16(len(items)))
			for _, it := range items {
				var err error
				if out, err = encodeFields(out, f.Sub, it); err != nil {
					return nil, err
				}
			}
		case "int8", "int16", "int32", "varint":
			n, err := toInt(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", f.Name, err)
			}
			switch f.Type {
			case "int8":
				out = append(out, byte(n))
			case "int16":
				out = binary.LittleEndian.AppendUint16(out, uint16(n))
			case "int32":
				out = binary.LittleEndian.AppendUint32(out, uint32(n))
			case "varint":
				u := uint64(n)
				for u >= 0x80 {
					out = append(out, byte(u)|0x80)
					u >>= 7
				}
				out = append(out, byte(u))
			}
		default:
			return nil, fmt.Errorf("%s: unsupported type %s", f.Name, f.Type)
		}
	}
	return out, nil
}

// DecodeExt 解码扩展协议 body；返回定义（未知协议为 nil）和字段。
func DecodeExt(d Dir, body []byte) (pid int, s *Spec, m Msg, err error) {
	if len(body) < 3 {
		return 0, nil, nil, fmt.Errorf("ext body too short (%d)", len(body))
	}
	pid = int(binary.LittleEndian.Uint16(body))*1000 + int(body[2])
	s = Lookup(d, pid)
	if s == nil {
		return pid, nil, nil, fmt.Errorf("unknown pid %d", pid)
	}
	m, off, err := decodeFields(s.Body, body, 3)
	if err == nil && off != len(body) {
		err = fmt.Errorf("%s.%s: %d trailing bytes", s.Module, s.Name, len(body)-off)
	}
	return pid, s, m, err
}

func decodeFields(tmpl []Field, b []byte, off int) (Msg, int, error) {
	m := Msg{}
	need := func(n int) error {
		if off+n > len(b) {
			return fmt.Errorf("short buffer at %d (+%d of %d)", off, n, len(b))
		}
		return nil
	}
	for _, f := range tmpl {
		switch f.Type {
		case "varstr", "buffer":
			if err := need(2); err != nil {
				return nil, off, err
			}
			n := int(binary.LittleEndian.Uint16(b[off:]))
			off += 2
			if err := need(n); err != nil {
				return nil, off, err
			}
			if f.Type == "varstr" {
				m[f.Name] = string(b[off : off+n])
			} else {
				m[f.Name] = append([]byte(nil), b[off:off+n]...)
			}
			off += n
		case "array":
			if err := need(2); err != nil {
				return nil, off, err
			}
			n := int(binary.LittleEndian.Uint16(b[off:]))
			off += 2
			items := make([]Msg, 0, n)
			for range n {
				it, o, err := decodeFields(f.Sub, b, off)
				if err != nil {
					return nil, o, err
				}
				off = o
				items = append(items, it)
			}
			m[f.Name] = items
		case "int8":
			if err := need(1); err != nil {
				return nil, off, err
			}
			m[f.Name] = int64(b[off])
			off++
		case "int16":
			if err := need(2); err != nil {
				return nil, off, err
			}
			m[f.Name] = int64(binary.LittleEndian.Uint16(b[off:]))
			off += 2
		case "int32":
			if err := need(4); err != nil {
				return nil, off, err
			}
			m[f.Name] = int64(int32(binary.LittleEndian.Uint32(b[off:])))
			off += 4
		case "varint":
			var v uint64
			for shift := 0; ; shift += 7 {
				if err := need(1); err != nil {
					return nil, off, err
				}
				c := b[off]
				off++
				v |= uint64(c&0x7f) << shift
				if c < 0x80 {
					break
				}
			}
			m[f.Name] = int64(v)
		default:
			return nil, off, fmt.Errorf("%s: unsupported type %s", f.Name, f.Type)
		}
	}
	return m, off, nil
}
