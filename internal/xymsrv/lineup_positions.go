package xymsrv

import (
	"strconv"

	"turnrpg/internal/xym"
)

// The original lineup protocol has uint8 spos1..3, although equipment bag
// positions are int32. The unused server-side s_talent fields carry high bytes
// for our client patch. Real talent IDs are below 32768; stored positions and
// equipment IDs remain unchanged. No protocol layouts or save formats change.
const lineupPositionFlag = 32768

func lineupSuitPos(m xym.Msg, i int) int {
	k := strconv.Itoa(i + 1)
	pos := int(msgInt(m, "spos"+k))
	tag := int(msgInt(m, "s_talent_"+k))
	if tag >= lineupPositionFlag && tag < lineupPositionFlag+256 {
		pos += (tag - lineupPositionFlag) * 256
	}
	return pos
}

func msgInt(m xym.Msg, key string) int64 {
	switch n := m[key].(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	}
	return 0
}

func lineupSlotMsg(pos int, sl *Slot) xym.Msg {
	m := xym.Msg{"pos": pos, "pet": sl.Pet, "mpos1": sl.Book[0], "mpos2": sl.Book[1]}
	for i, p := range sl.Suit {
		k := strconv.Itoa(i + 1)
		m["spos"+k] = p % 256
		if p > 255 {
			m["s_talent_"+k] = lineupPositionFlag + p/256
		}
	}
	return m
}

// An unpatched/stale client echoes truncated positions from old lineup data.
// Refuse the whole save rather than replacing known high-position equipment.
func (p *Player) staleLineupPositions(lu *Lineup, rows []xym.Msg) bool {
	if lu == nil {
		return false
	}
	for _, row := range rows {
		pos := int(msgInt(row, "pos"))
		if pos < 1 || pos > len(lu.Slots) || lu.Slots[pos-1] == nil {
			continue
		}
		for i, old := range lu.Slots[pos-1].Suit {
			if old > 255 && msgInt(row, "s_talent_"+strconv.Itoa(i+1)) < lineupPositionFlag {
				return true
			}
		}
	}
	return false
}
