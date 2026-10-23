| 包 | 作用 |
|---|---|
| `internal/xym` | 帧 / 扩展协议编解码 |
| `internal/gamedata` | 静态表（CSV + JSON，go:embed） |
| `internal/battle` | 战斗模拟与战报（`Simulate`、`DecodeRecord`、`Replay`） |
| `internal/store` | 用户存档（GORM + SQLite；兜底 JSON 文件） |
| `internal/xymsrv` | 网关、会话、各玩法 handler |

- 存档（`internal/store`）：账号、角色各一张表；玩家玩法存档（卡牌、阵容、道具、关卡进度）整体存为 JSON 一列（结构还在频繁变动）。业务侧全部缓存在内存，每次修改立即落库。表结构即 `internal/store/sqlite.go` 里的三个 GORM 模型；启动时只建缺的表、补缺的列，不用 AutoMigrate（它在 SQLite 上会重建整张表）。
- 静态数据：见下方「静态表」。
- 闯关与战斗：`h_task.go`（关卡列表、开战、结算）、`internal/battle`：`battle.go`（回合流程、前后阵补位、战意、命令流输出）、`battle_skill.go`（法术 17 种目标与 13 种特效，按客户端 `magic_mgr.lua` 的公式；神通 96 族，按 `talent.lua` 描述实现）、`record.go`（战报解码与回放校验）。`TestSimulateAllGates` 用全部关卡阵容回放校验战报自洽，`TestTalentCoverage` 报告新手期神通覆盖率；`analysis/tools/fight_record.py` 可解码官方战报对照。
- 升级：`data/exp.json` 由 `export_tables.py` 导出——卡牌取 client2 `logic/pet_mgr.lua` 的 `_PET_UPGRADE_EXP_LIST`×星级倍率（1~100 级，与 `数据.xlsx` 一致、抓包核对过），人物取 `view/CMainView.lua` 的 `upgradeNeedExp`（满级 50）。卡牌等级 ≤ 人物等级×2。
- 神通校准：`analysis/tools/talent_check.py` 回放官方战报，把各族神通带出的数值与攻击/上限等基数相比；描述与实测不符的以实测为准（代码里有注释）。
- 背包：`bag.go`（道具叠放、装备/法术书、奖励表掉落编码 `前缀+编号:万分比:数量`、魂魄、回包）、`h_bag.go`（拉取、使用道具、穿脱、出售）、`box.go`（宝箱：宝箱和钥匙任一件都能开，十连 `C_OPEN_TEN_BOX`；黄金宝箱掉落分布取自抓包的 5 次十连，白银/黑铁按道具描述估计）。奖励界面不显示 `cash` 字段，银两以展示道具 20010 放进 list。关卡胜利按奖励表掉落（第一关必掉 1 张 1 星卡）。
- 碎片与强化：`parts.go`（规则：经验表、材料经验、碎片合成数，全部取自客户端 suit_mgr / magic_mgr / 两个强化界面）、`h_parts.go`（装备强化 C_REFINE_SUITS、法术 / 天生法术强化、碎片合成、拆碎、碎片转化）。装备碎片 = 客户端的 suit_iron（掉落编码 JT），法术碎片 = book_part（SP）；三/四/五星通用碎片是道具。修为强化（from_xiuwei）、天匠融合未做。
- 本服 hotfix（`hotfix-local/`，文件名版本号须为 1.5.403）：01 修招募滚动报错；02 修纹理缓存告警清掉正在等回包的界面回调（强化后界面不复位）。`hotfix-debug/` 为排查用的 Lua 错误/信号日志，不要常驻。
- 抓包回放：`XYM_CAPTURES=<绝对路径>/analysis/captures go test ./internal/xymsrv -run Replay -v`，把官方请求原样发给本服，回包须在官方回包之内。

## 静态表

`internal/gamedata/data/` 下的文件编译时嵌入二进制（go:embed），启动时加载。

- **CSV（可手改，唯一数据源）**：`pet.csv`、`magic.csv`、`suit.csv`、`talent.csv`、`talentset.csv`。UTF-8 带 BOM，Excel 可直接打开；列表字段用 `;` 分隔。列含义和 ID 规律见 [docs/DATA.md](docs/DATA.md)。
  改数据：编辑 CSV → 重新编译 / `go run` 启动即生效（解析出错时启动即报出表名、行号、列名）。
  从客户端重新生成：`python -I ../analysis/tools/export_tables.py --csv`（**会覆盖手改**）。
- **JSON（导出产物，不手改）**：`gates.json`（关卡、阵型、怪物、奖励）、`bag.json`（道具）、`exp.json`、`maps.json`、`static.json`、`client_locks.json`，由 `python -I ../analysis/tools/export_tables.py` 从客户端配置表和官方抓包导出（不带 `--csv` 时不碰 CSV）。
