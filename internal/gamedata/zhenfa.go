package gamedata

import (
	"strconv"
	"strings"
)

// 阵法（zhenfa.json ← 客户端 auto/zhenfa.lua，analysis/tools/export_tables.py --zhenfa）。
// 八阵：1 天覆 2 地载 3 风扬 4 云垂 5 龙飞 6 虎翼 7 鸟翔 8 蛇蟠；每阵五阶，表 id = 类型×100 + 阶。

// 阵位加成的属性（logic/zhenfa_mgr.lua ZHENFA_*）。数值都是固定值：基础 + 阵位等级 × 每级（先攻只取基础）。
const (
	ZfPhysicAdd = 1 // 造成物理伤害结果处增加
	ZfPhysicSub = 2 // 受到物理伤害结果处减少
	ZfMagicAdd  = 3 // 造成法术伤害结果处增加
	ZfMagicSub  = 4 // 受到法术伤害结果处减少
	ZfSpeed     = 5 // 先攻值（可为负）
	ZfHP        = 6 // 初始生命上限增加
	ZfAtt       = 7 // 初始攻击力增加
	ZfLoseTal   = 8 // 失去神通（每级字段是神通号）
)

const (
	ZhenfaTypes  = 8
	ZhenfaStairs = 5
	ZhenfaMaxLv  = 10 // 阵位等级上限
)

// ZfAdd 是阵位的一项加成：属性、基础值、每级增加。
type ZfAdd struct{ Kind, Base, Per int }

// Value 是阵位等级 lv 时的数值（先攻不随等级变）。
func (a ZfAdd) Value(lv int) int {
	if a.Kind == ZfSpeed {
		return a.Base
	}
	return a.Base + lv*a.Per
}

type ZhenfaInfo struct {
	Name        string    `json:"name"`
	Eye         int       `json:"eye"`        // 阵眼阵位 1–5
	EyeRate     int       `json:"eye_rate"`   // 阵眼技释放概率 %
	EyeRounds   []int     `json:"eye_rounds"` // 这些回合结束时判定
	EyeLv       int       `json:"eye_lv"`     // 阵眼技法术等级
	EyeSkill    int32     `json:"eye_skill"`  // 阵眼技法术号
	EyeBuff     int32     `json:"eye_buff"`   // 阵眼技附带的 buff（0 = 无）
	EyeBuffRate int       `json:"eye_buff_rate"`
	Kezhi       []int     `json:"kezhi"` // 本阵克制的阵法类型
	PosAddRaw   [5]string `json:"pos_add"`
	FightPoint  [2]int    `json:"fight_point"` // 战力：基础 + 每阵位等级
	Enhance     [10]int   `json:"enhance"`     // 阵位 lv → lv+1 的经验

	PosAdd [5][]ZfAdd `json:"-"`
}

// Zhenfa 是阵法表（id = 类型×100 + 阶）。
var Zhenfa map[int]*ZhenfaInfo

// ZhenfaOf 是 typ 阵法第 stair 阶（没有返回 nil）。
func ZhenfaOf(typ, stair int) *ZhenfaInfo { return Zhenfa[typ*100+stair] }

// Counters：a 阵克制 b 阵。
func Counters(a, b int) bool {
	if z := ZhenfaOf(a, 1); z != nil {
		for _, k := range z.Kezhi {
			if k == b {
				return true
			}
		}
	}
	return false
}

func loadZhenfa() {
	MustLoad("zhenfa.json", &Zhenfa)
	for _, z := range Zhenfa {
		for i, raw := range z.PosAddRaw {
			for _, part := range strings.Split(raw, "|") {
				f := strings.Split(part, ":")
				if len(f) != 3 {
					continue
				}
				k, _ := strconv.Atoi(f[0])
				x, _ := strconv.Atoi(f[1])
				y, _ := strconv.Atoi(f[2])
				z.PosAdd[i] = append(z.PosAdd[i], ZfAdd{k, x, y})
			}
		}
	}
}
