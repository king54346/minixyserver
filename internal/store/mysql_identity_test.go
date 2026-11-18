package store

import (
	"encoding/json"
	"testing"
)

func TestSplitPlayerIdentityAndUnknownFields(t *testing.T) {
	data := []byte(`{ "uid":17,
	"pets":[{"npcnum":9007199254740993,"NPCNUM":2,"unknown":{"text":"keep A B"}},null,{"npc\u006eum":3}],
	"items":[{"pos":4,"Pos":9}], "suits":[{"itemnum":5,"future":[1,null]}],
	"books":[{"pos":6}], "lineups":[null,{"no":7}], "future":{"x":"a b"}}`)
	s, err := splitPlayer(17, data)
	if err != nil {
		t.Fatal(err)
	}
	for table, keys := range map[string][]string{
		"player_pets":  {"npcnum:9007199254740993", "slot:1", "npcnum:3"},
		"player_items": {"pos:4"}, "player_suits": {"itemnum:5"}, "player_books": {"pos:6"}, "player_lineups": {"slot:0", "no:7"},
	} {
		rows := s.entities[table]
		if len(rows) != len(keys) {
			t.Fatalf("%s: %d rows", table, len(rows))
		}
		for i, key := range keys {
			if rows[i].EntityKey != key || !json.Valid([]byte(rows[i].Data)) {
				t.Fatalf("%s[%d]: %+v", table, i, rows[i])
			}
		}
	}
	if s.entities["player_pets"][0].Data != `{"npcnum":9007199254740993,"NPCNUM":2,"unknown":{"text":"keep A B"}}` {
		t.Fatal("unknown fields or large integer changed")
	}
	for _, bad := range []string{`{"pets":[[]]}`, `{"items":[123]}`, `{"suits":["x"]}`, `{"books":[{"pos":1},{"pos":1}]}`} {
		if _, err := splitPlayer(17, []byte(bad)); err == nil {
			t.Fatalf("accepted malformed save %s", bad)
		}
	}
}
