package gamedata

// SoulTransform 是 4 / 5 星魂魄转 3 星魂魄（客户端 auto/soul_transform.lua：每个转成 Count 个 To 的魂魄，
// 每个花 Gold 元宝；导出到 data/soul_transform.json）。
type SoulTransform struct {
	To    int32 `json:"to"`
	Count int32 `json:"count"`
	Gold  int32 `json:"gold"`
}

// SoulTransforms：护法类型 → 转化规则。
var SoulTransforms map[int32]SoulTransform

func init() { MustLoad("soul_transform.json", &SoulTransforms) }
