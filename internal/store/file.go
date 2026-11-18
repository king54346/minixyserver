package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// File 是原来的 JSON 文件存档（格式不变）：
//
//	accounts.json        {"next_uid": N, "accounts": {"<小写账号名>": Account}}
//	roles.json           {"roles": {"<uid>": Role}}
//	players/<uid>.json   玩家存档
//	globals/<name>.json  全服数据
//	records/<kind>/<key>.json  录像
//
// 每个文件原子写（先写 .tmp 再改名）。账号 / 角色是整表一个文件，所以这里缓存一份，保存单条时整表重写。
type File struct {
	dir string

	mu       sync.Mutex
	accounts map[string]Account // key: AccountKey
	roles    map[int32]Role
}

func OpenFile(dir string) *File { return &File{dir: dir} }

func (f *File) Close() error { return nil }

type accountsFile struct {
	NextUID int32              `json:"next_uid"`
	ByURS   map[string]Account `json:"accounts"`
}

type rolesFile struct {
	ByUID map[int32]Role `json:"roles"`
}

// readJSON 读一个 JSON 文件；文件不存在返回 false。
func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

// writeJSON 原子写：先写 .tmp 再改名，进程中途被杀也不会留下半个文件。
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (f *File) loadAccountsLocked() error {
	if f.accounts != nil {
		return nil
	}
	var af accountsFile
	if _, err := readJSON(filepath.Join(f.dir, "accounts.json"), &af); err != nil {
		return err
	}
	f.accounts = map[string]Account{}
	for _, a := range af.ByURS {
		f.accounts[AccountKey(a.URS)] = a
	}
	return nil
}

func (f *File) LoadAccounts() ([]Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.loadAccountsLocked(); err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(f.accounts))
	for _, a := range f.accounts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func (f *File) SaveAccount(a Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.loadAccountsLocked(); err != nil {
		return err
	}
	f.accounts[AccountKey(a.URS)] = a
	af := accountsFile{NextUID: 20001, ByURS: f.accounts}
	for _, x := range f.accounts {
		af.NextUID = max(af.NextUID, x.UID+1)
	}
	return writeJSON(filepath.Join(f.dir, "accounts.json"), af)
}

func (f *File) loadRolesLocked() error {
	if f.roles != nil {
		return nil
	}
	var rf rolesFile
	if _, err := readJSON(filepath.Join(f.dir, "roles.json"), &rf); err != nil {
		return err
	}
	f.roles = map[int32]Role{}
	for uid, r := range rf.ByUID {
		f.roles[uid] = r
	}
	return nil
}

func (f *File) LoadRoles() ([]Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.loadRolesLocked(); err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(f.roles))
	for _, r := range f.roles {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func (f *File) SaveRole(r Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.loadRolesLocked(); err != nil {
		return err
	}
	f.roles[r.UID] = r
	return writeJSON(filepath.Join(f.dir, "roles.json"), rolesFile{ByUID: f.roles})
}

func (f *File) playerPath(uid int32) string {
	return filepath.Join(f.dir, "players", strconv.Itoa(int(uid))+".json")
}

func (f *File) LoadPlayer(uid int32) ([]byte, error) {
	data, err := os.ReadFile(f.playerPath(uid))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

func (f *File) SavePlayer(uid int32, save []byte) error { return writeRaw(f.playerPath(uid), save) }

// playerUIDs 列出 players/ 下已有存档的 uid（迁移用）。
func (f *File) playerUIDs() ([]int32, error) {
	ents, err := os.ReadDir(filepath.Join(f.dir, "players"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []int32
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		uid, err := strconv.Atoi(name[:len(name)-len(".json")])
		if err != nil {
			continue // 不是 <uid>.json（例如残留的 .tmp）
		}
		out = append(out, int32(uid))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// ---- 全服数据：globals/<name>.json（旧版按保留 uid 存在 players/<uid>.json，读不到新文件时回退去读）----

func (f *File) globalPath(name string) string { return filepath.Join(f.dir, "globals", name+".json") }

func (f *File) LoadGlobal(name string) ([]byte, error) {
	data, err := os.ReadFile(f.globalPath(name))
	if errors.Is(err, os.ErrNotExist) {
		if uid := LegacyGlobalUID(name); uid >= 0 {
			return f.LoadPlayer(uid)
		}
		return nil, ErrNotFound
	}
	return data, err
}

func (f *File) SaveGlobal(name string, data []byte) error { return writeRaw(f.globalPath(name), data) }

// ---- 录像：records/<kind>/<key>.json ----

func (f *File) recordPath(kind string, key int64) string {
	return filepath.Join(f.dir, "records", kind, strconv.FormatInt(key, 10)+".json")
}

func (f *File) LoadRecord(kind string, key int64) ([]byte, error) {
	data, err := os.ReadFile(f.recordPath(kind, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

func (f *File) SaveRecord(kind string, key int64, _ int32, data []byte) error {
	return writeRaw(f.recordPath(kind, key), data)
}

func (f *File) ListRecords(kind string) ([]Record, error) {
	ents, err := os.ReadDir(filepath.Join(f.dir, "records", kind))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range ents {
		name := e.Name()
		key, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
		if e.IsDir() || filepath.Ext(name) != ".json" || err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(f.dir, "records", kind, name))
		if err != nil {
			return nil, err
		}
		out = append(out, Record{Kind: kind, Key: key, Owner: ownerOf(string(data)), Data: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// writeRaw 原子写原样字节（先写 .tmp 再改名）。
func writeRaw(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
