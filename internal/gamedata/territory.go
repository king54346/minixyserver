package gamedata

// 洞府领土争夺的地图数据（从客户端 auto/dongfu_territory_gate*.lua 抽出）。
// 8 个战场（station 1-8），每个 16 个据点（X/Y 出生点、M/N 祭坛、A-F 三档擂台、
// G-J 低级/高级怪物、W 妖王、Z 奇异仙阵）和一张邻接表（移动耗时）。

type TerritoryGate struct {
	ID    int    `json:"id"`
	Key   string `json:"key"`
	Kind  string `json:"kind"`  // spawn / altar / arena / monster / boss / array
	Name  string `json:"name"`  // 中文名
	Owner int    `json:"owner"` // 开场归属：0 攻方、1 守方、-1 中立
}

type TerritoryEdge struct {
	A    int `json:"a"`
	B    int `json:"b"`
	Time int `json:"time"`
}

type TerritoryStation struct {
	ID    int             `json:"id"`
	Gates []TerritoryGate `json:"gates"`
	Edges []TerritoryEdge `json:"edges"`
}

var Territory struct {
	Stations []TerritoryStation `json:"stations"`
}

func init() { MustLoad("territory.json", &Territory) }

// TerritoryStationByID 取一个战场配置；没有返回 nil。
func TerritoryStationByID(id int) *TerritoryStation {
	for i := range Territory.Stations {
		if Territory.Stations[i].ID == id {
			return &Territory.Stations[i]
		}
	}
	return nil
}
