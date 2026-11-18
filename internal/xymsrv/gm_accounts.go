package xymsrv

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"turnrpg/internal/store"
)

const accountUIDFloor = "account_uid_floor"

func (a *Accounts) List() []Account {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Account, 0, len(a.ByURS))
	for _, acc := range a.ByURS {
		out = append(out, *acc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out
}

func (a *Accounts) SetPassword(urs, pwd string) (*Account, error) {
	if !validNamePwd(urs, pwd) {
		return nil, ErrInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.ByURS[key(urs)]
	if old == nil {
		return nil, ErrNoAccount
	}
	acc := *old
	// Reuse the salt to preserve the separately hashed security code.
	salt, err := hex.DecodeString(acc.PwdSalt)
	if err != nil || len(salt) == 0 {
		return nil, errors.New("invalid account salt")
	}
	acc.PwdHash = hashPwd(salt, pwd)
	acc.SID = ""
	if err := a.saveLocked(&acc); err != nil {
		return nil, err
	}
	a.ByURS[key(urs)] = &acc
	delete(a.sidIssued, acc.UID)
	return &acc, nil
}

// Keep the UID high-water mark before deletion so stale global references can never
// refer to a newly registered account after restart.
func (srv *Server) gmDeleteAccountData(urs, confirm string) (int32, error) {
	if confirm == "" || confirm != urs {
		return 0, errors.New("confirm does not match urs")
	}
	var uid int32
	for _, acc := range srv.accounts.List() {
		if key(acc.URS) == key(urs) {
			uid = acc.UID
			break
		}
	}
	if uid == 0 {
		return 0, ErrNoAccount
	}
	if err := srv.kickWait(uid); err != nil {
		return 0, err
	}
	a := srv.accounts
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := a.ByURS[key(urs)]
	if acc == nil || acc.UID != uid {
		return 0, ErrNoAccount
	}
	if srv.sessionOf(uid) != nil {
		return 0, errors.New("account reconnected; retry after disconnect")
	}
	deleter, ok := a.st.(store.AccountDeleter)
	if !ok {
		return 0, errors.New("store does not support account deletion")
	}
	roleLock := srv.roles.updateLock(uid)
	roleLock.Lock()
	defer roleLock.Unlock()
	srv.players.mu.Lock()
	defer srv.players.mu.Unlock()
	p := srv.players.byUID[uid]
	if p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
	}
	data, _ := json.Marshal(a.NextUID)
	if err := a.st.SaveGlobal(accountUIDFloor, data); err != nil {
		return 0, err
	}
	if err := deleter.DeleteAccountData(uid); err != nil {
		return 0, err
	}
	delete(a.ByURS, key(urs))
	delete(a.sidIssued, uid)
	srv.roles.mu.Lock()
	delete(srv.roles.ByUID, uid)
	srv.roles.mu.Unlock()
	delete(srv.players.byUID, uid)
	return uid, nil
}
