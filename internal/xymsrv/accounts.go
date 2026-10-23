package xymsrv

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

// 账号规则照 offline_probe.lua 的注释（服务端 backend/accounts.py 的口径）：
//   - 账号名 1-32 个字符，密码 1-64 个字符，否则 101；
//   - 安全码 4-16 位字母或数字，不分大小写，不能和密码一样；
//   - 登录：没有账号 102，密码错 1。
//
// 「一台设备只能注册一个号」「按 IP 限量」属于原服运营规则，本地服不实现（见 docs）。
const (
	maxURS = 32
	maxPwd = 64
)

var secCodeRe = regexp.MustCompile(`^[A-Za-z0-9]{4,16}$`)

// Account 与 store.Account 字段逐一对应（存取时直接做类型转换）。
type Account struct {
	UID      int32     `json:"uid"`
	URS      string    `json:"urs"`
	PwdSalt  string    `json:"pwd_salt"`
	PwdHash  string    `json:"pwd_hash"`
	CodeHash string    `json:"code_hash,omitempty"` // 安全码（转小写后）的哈希
	Device   string    `json:"device,omitempty"`    // 最近一次登录的设备号
	Created  time.Time `json:"created"`
	SID      string    `json:"sid,omitempty"` // 最近一次发给客户端的 sid（断线重连用）
}

// Accounts 是全部账号（内存缓存，修改后立即写入 store）。
type Accounts struct {
	mu      sync.Mutex
	st      store.Store
	NextUID int32
	ByURS   map[string]*Account // key: 小写账号名
}

var (
	ErrInvalid     = errors.New("invalid account or password")
	ErrNameTaken   = errors.New("account name taken")
	ErrNoAccount   = errors.New("no such account")
	ErrBadPassword = errors.New("bad password")
	ErrBadCode     = errors.New("bad security code")
)

// OpenAccounts 从 st 读入全部账号。新账号 uid 从 20001 起，接在已有最大 uid 之后。
func OpenAccounts(st store.Store) (*Accounts, error) {
	list, err := st.LoadAccounts()
	if err != nil {
		return nil, err
	}
	a := &Accounts{st: st, NextUID: 20001, ByURS: map[string]*Account{}}
	for _, x := range list {
		acc := Account(x)
		a.ByURS[key(acc.URS)] = &acc
		a.NextUID = max(a.NextUID, acc.UID+1)
	}
	return a, nil
}

func (a *Accounts) saveLocked(acc *Account) error { return a.st.SaveAccount(store.Account(*acc)) }

func key(urs string) string { return store.AccountKey(urs) }

func validNamePwd(urs, pwd string) bool {
	n, p := utf8.RuneCountInString(strings.TrimSpace(urs)), utf8.RuneCountInString(pwd)
	return n >= 1 && n <= maxURS && p >= 1 && p <= maxPwd
}

func hashPwd(salt []byte, pwd string) string {
	h, err := pbkdf2.Key(sha256.New, pwd, salt, 100_000, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(h)
}

func checkCode(acc *Account, code string) bool {
	if acc.CodeHash == "" {
		return false
	}
	salt, _ := hex.DecodeString(acc.PwdSalt)
	return subtle.ConstantTimeCompare([]byte(hashPwd(salt, "code:"+strings.ToLower(code))), []byte(acc.CodeHash)) == 1
}

// Register 注册账号（帧 20）。code 为空表示不设安全码（命令行 adduser 可用）。
func (a *Accounts) Register(urs, pwd, code string) (*Account, error) {
	if !validNamePwd(urs, pwd) {
		return nil, ErrInvalid
	}
	if code != "" && (!secCodeRe.MatchString(code) || strings.EqualFold(code, pwd)) {
		return nil, ErrBadCode
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	k := key(urs)
	if _, ok := a.ByURS[k]; ok {
		return nil, ErrNameTaken
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	acc := &Account{UID: a.NextUID, URS: strings.TrimSpace(urs), PwdSalt: hex.EncodeToString(salt),
		PwdHash: hashPwd(salt, pwd), Created: time.Now()}
	if code != "" {
		acc.CodeHash = hashPwd(salt, "code:"+strings.ToLower(code))
	}
	a.NextUID++
	a.ByURS[k] = acc
	return acc, a.saveLocked(acc)
}

// Login 校验账号密码（帧 2）。
func (a *Accounts) Login(urs, pwd, device string) (*Account, error) {
	if !validNamePwd(urs, pwd) {
		return nil, ErrInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	acc, ok := a.ByURS[key(urs)]
	if !ok {
		return nil, ErrNoAccount
	}
	salt, _ := hex.DecodeString(acc.PwdSalt)
	if subtle.ConstantTimeCompare([]byte(hashPwd(salt, pwd)), []byte(acc.PwdHash)) != 1 {
		return nil, ErrBadPassword
	}
	if device != "" {
		acc.Device = device
	}
	return acc, a.saveLocked(acc)
}

// NewSID 给账号发一个新 sid（S_SEND_SID，32 个十六进制字符，与原服长度一致）。
func (a *Accounts) NewSID(acc *Account) (string, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	a.mu.Lock()
	defer a.mu.Unlock()
	acc.SID = hex.EncodeToString(b)
	return acc.SID, a.saveLocked(acc)
}

// BySID 断线重连（帧 6）：uid 与 sid 都对上才算。
func (a *Accounts) BySID(uid uint32, sid string) *Account {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, acc := range a.ByURS {
		if uint32(acc.UID) == uid && acc.SID != "" &&
			subtle.ConstantTimeCompare([]byte(acc.SID), []byte(sid)) == 1 {
			return acc
		}
	}
	return nil
}

// Recover 找回密码（帧 21）：newPwd 为空只核对安全码（104），否则重设密码（105）。
func (a *Accounts) Recover(urs, code, newPwd string) (uint32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acc, ok := a.ByURS[key(urs)]
	if !ok {
		return 0, ErrNoAccount
	}
	if !checkCode(acc, code) {
		return 0, ErrBadCode
	}
	if newPwd == "" {
		return xym.RecoverCodeOK, nil
	}
	if !validNamePwd(urs, newPwd) || strings.EqualFold(newPwd, code) {
		return 0, ErrInvalid
	}
	salt, _ := hex.DecodeString(acc.PwdSalt)
	acc.PwdHash = hashPwd(salt, newPwd)
	return xym.RecoverDone, a.saveLocked(acc)
}
