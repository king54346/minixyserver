package xym

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// 主命令（net.lua MAIN_PROC / login.lua NET_CMD_* / offline_probe.lua 帧 20、21）。
const (
	MainExt      = 1   // 双向：扩展协议
	MainExtLarge = 254 // 客户端发：长度为 u16 的扩展协议

	// 服务端 -> 客户端
	SCreate    = 2 // login.S_CREATE：账号还没有角色，客户端进入起名/开场剧情
	SSeed      = 3 // net.S_SEED：u32 RC4 种子，0 = 不加密
	SVersion   = 4 // login.S_VERSION
	SMsg       = 5 // net.S_MSG：弹一个文本框（UTF-8）
	SVerError  = 6 // login.S_VER_ERROR
	SLoginFail = 7 // login.S_LOGIN_FAIL：u32 错误码
	SPrintData = 255

	// 客户端 -> 服务端
	CLogin    = 2  // 版本 3×u16 + varstr 账号 + varstr 密码 [+ varstr 设备号]
	CCreate   = 3  // u16 头像 + varstr 名字
	CDelUser  = 5  // 空
	CSidLogin = 6  // 版本 3×u16 + u32 uid + varstr sid
	CNewLogin = 7  // 版本 3×u16
	CRegister = 20 // 私服：同 CLogin 再接 varstr 安全码
	CRecover  = 21 // 私服：版本 + varstr 账号 + varstr 安全码 + varstr 新密码 + varstr 设备号
	CNtLogin  = 12
	CNtLenovo = 13 // 长度字段为 u16
)

// 登录失败码（S_LOGIN_FAIL 的 u32，含义见 offline_probe.lua:376-413、537-581）。
const (
	FailBadPassword = 1   // 原版 LOGIN_ERR_CODE_MSG[1]：账号密码有误
	FailNameTaken   = 100 // 注册：账号名已被注册
	FailInvalid     = 101 // 账号名或密码不合法（账号 ≤32，密码 ≤64，均非空）
	FailNoAccount   = 102 // 没有这个账号：客户端询问是否注册
	FailRefused     = 103 // 拒绝（停止注册/登录等），服务端先用 SMsg 说明原因
	RecoverCodeOK   = 104 // 找回第一步：安全码正确
	RecoverDone     = 105 // 找回第二步：密码已重设
)

// ReadC2S 从客户端读一帧。客户端长度字段：main 254/13 为 u16，其余为 u8（net.lua send_prot）。
func ReadC2S(r *bufio.Reader) (main byte, body []byte, err error) {
	main, err = r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var n int
	if main == MainExtLarge || main == CNtLenovo {
		var hdr [2]byte
		if _, err = io.ReadFull(r, hdr[:]); err != nil {
			return 0, nil, err
		}
		n = int(binary.LittleEndian.Uint16(hdr[:]))
	} else {
		c, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n = int(c)
	}
	body = make([]byte, n)
	if _, err = io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return main, body, nil
}

// AppendS2C 追加一帧服务端消息：[main u8][len u16][body]。
func AppendS2C(dst []byte, main byte, body []byte) []byte {
	if len(body) > 0xffff {
		panic(fmt.Sprintf("xym: s2c frame too large: %d", len(body)))
	}
	dst = append(dst, main)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(body)))
	return append(dst, body...)
}

// ReadS2C 从服务端读一帧（代理/测试用）。
func ReadS2C(r *bufio.Reader) (main byte, body []byte, err error) {
	var hdr [3]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	body = make([]byte, binary.LittleEndian.Uint16(hdr[1:]))
	if _, err = io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return hdr[0], body, nil
}

// AppendC2S 追加一帧客户端消息（测试/机器人用）。
func AppendC2S(dst []byte, main byte, body []byte) []byte {
	dst = append(dst, main)
	if main == MainExtLarge || main == CNtLenovo {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(len(body)))
	} else {
		if len(body) > 0xff {
			panic(fmt.Sprintf("xym: c2s frame main=%d too large: %d", main, len(body)))
		}
		dst = append(dst, byte(len(body)))
	}
	return append(dst, body...)
}

// Reader 是按顺序读小端字段的小工具（解析登录等裸帧）。
type Reader struct {
	B   []byte
	Off int
	Err error
}

func (r *Reader) need(n int) bool {
	if r.Err == nil && r.Off+n > len(r.B) {
		r.Err = fmt.Errorf("short frame: need %d at %d of %d", n, r.Off, len(r.B))
	}
	return r.Err == nil
}

func (r *Reader) U16() int {
	if !r.need(2) {
		return 0
	}
	v := binary.LittleEndian.Uint16(r.B[r.Off:])
	r.Off += 2
	return int(v)
}

func (r *Reader) U32() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(r.B[r.Off:])
	r.Off += 4
	return v
}

func (r *Reader) Str() string {
	n := r.U16()
	if !r.need(n) {
		return ""
	}
	s := string(r.B[r.Off : r.Off+n])
	r.Off += n
	return s
}

// More 表示后面还有未读字节（例如可选的设备号）。
func (r *Reader) More() bool { return r.Err == nil && r.Off < len(r.B) }

// U32LE 编码一个 u32（S_SEED / S_LOGIN_FAIL 的 body）。
func U32LE(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// VarStr 编码 u16 长度 + 字节。
func VarStr(s string) []byte {
	return append(binary.LittleEndian.AppendUint16(nil, uint16(len(s))), s...)
}
