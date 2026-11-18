package gamedata

// 西天取经的静态数据（data/qujing.json，analysis/tools/export_qujing.py 从官方抓包导出）。

// QujingMember 是关卡 / 阵容模板里的一名护法。关卡模板是客户端怪物表（fight_npc）的怪：属性 =
// HP + HPAdd × 参数 / 1000（攻击同理）；PVP 阵容模板没有这几项，按护法自身的基础属性算。
type QujingMember struct {
	Name    string  `json:"name,omitempty"`
	Talents []int32 `json:"talents"`
	Pos     int     `json:"pos"`
	Speed   int     `json:"speed"`
	Race    int     `json:"race,omitempty"`
	MagicLv int     `json:"magic_lv"`
	NPC     int32   `json:"npc,omitempty"`
	Type    int32   `json:"type"`
	Att     int32   `json:"att,omitempty"`
	HP      int32   `json:"hp,omitempty"`
	AttAdd  int32   `json:"attadd,omitempty"`
	HPAdd   int32   `json:"hpadd,omitempty"`
	Magic   int32   `json:"magic"`
	Boss    bool    `json:"boss,omitempty"`
}

// QujingStage 是第 Stage 劫第 Sub 难的敌方（NPC 关 1/4、特殊 AI 关 7、打魂关 8、首领 9；官方阵型）；
// Param 是困难难度的属性参数。
type QujingStage struct {
	Stage   int            `json:"stage"`
	Sub     int            `json:"sub"`
	Param   int            `json:"param"`
	Name    string         `json:"name"`
	Desc    string         `json:"desc"`
	Icon    int32          `json:"icon"`
	Members []QujingMember `json:"members"`
}

// QujingPvp 是玩家关（2/3/5/6 难）用的玩家阵容模板（三份抓包里的主流 PVP 阵容）。
type QujingPvp struct {
	Archetype string         `json:"archetype"`
	Members   []QujingMember `json:"members"`
}

// QujingGood 是经书兑换的一项商品（S_QUJING_OPEN_GOODS_LIST.list；Gold -1 = 不能用元宝买）。
type QujingGood struct {
	Pos     int    `json:"pos"`
	ID      int32  `json:"id"`
	Type    int    `json:"type"`
	Name    string `json:"name"`
	Limit   int    `json:"limit"` // 0 = 无每周兑换次数限制
	Jingshu int    `json:"jingshu"`
	Gold    int    `json:"gold"`
	Count   int32  `json:"count,omitempty"` // 每份物品数量
}

// Qujing 是西天取经的全部静态数据。
var Qujing struct {
	Stages []QujingStage            `json:"stages"`
	NPCs   map[string]*QujingMember `json:"npcs"` // 首领变身用的怪（npc 号 → 怪）
	Pvp    []QujingPvp              `json:"pvp"`
	Shop   []QujingGood             `json:"shop"`
}

func init() { MustLoad("qujing.json", &Qujing) }
