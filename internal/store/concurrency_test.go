package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSQLiteConcurrentPlayers(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "xym.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const users, updates = 16, 30
	var wg sync.WaitGroup
	for n := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			uid := int32(20001 + n)
			for version := range updates {
				data := []byte(fmt.Sprintf(`{"uid":%d,"version":%d,"payload":"%s"}`, uid, version, bytes.Repeat([]byte("x"), 8192)))
				if err := db.SavePlayer(uid, data); err != nil {
					t.Error(err)
					return
				}
				got, err := db.LoadPlayer(uid)
				if err != nil || !bytes.Equal(got, data) || !json.Valid(got) {
					t.Errorf("uid %d: stale/corrupt read: %v", uid, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	for n := range users {
		data, err := db.LoadPlayer(int32(20001 + n))
		var got struct{ Version int }
		if err != nil || json.Unmarshal(data, &got) != nil || got.Version != updates-1 {
			t.Fatalf("lost write for user %d: %s, %v", n, data, err)
		}
	}
}

// Compare the old shared connection with WAL's independent read pool using
// concurrent players, each writing one snapshot and reading it three times.
func BenchmarkSQLiteMultiplayer(b *testing.B) {
	for _, separate := range []bool{false, true} {
		name := "shared_connection"
		if separate {
			name = "separate_read_pool"
		}
		b.Run(name, func(b *testing.B) {
			db, err := OpenSQLite(filepath.Join(b.TempDir(), "xym.db"))
			if err != nil {
				b.Fatal(err)
			}
			reader := db.readDB
			if !separate {
				db.readDB = db.db
			}
			b.Cleanup(func() { db.readDB = reader; db.Close() })
			payload := bytes.Repeat([]byte("x"), 32*1024)
			var next atomic.Int32
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				uid := 20000 + next.Add(1)
				for pb.Next() {
					if err := db.SavePlayer(uid, payload); err != nil {
						b.Error(err)
						return
					}
					for range 3 {
						if _, err := db.LoadPlayer(uid); err != nil {
							b.Error(err)
							return
						}
					}
				}
			})
		})
	}
}
