# 维护工具

所有命令默认在server目录执行。

| 入口 | 用途 |
| --- | --- |
| `build_first_test_apk.py` | 按first/second区服构建并验证APK，名称为历史保留，两个区服都支持 |
| `sync_client_fixes.py` | 校验或生成客户端内置修复与同源兼容补丁 |
| `patch_battle_detail.py` | 应用或校验燃烧状态显示的受控源码修改；APK构建依赖 |
| `run_client_tests.py` | 运行tests目录中的全部Lua回归 |
| `tests/` | 等待锁、重连、战报、阵容、强化、灵穴和报价等客户端回归 |
| `ssh.py` | SSH上传、下载及执行命令，部署工具依赖 |
| `perfect_remote.py` | 显式调用管理接口修改指定角色；会改变角色存档，不自动运行 |
| `patch_material_prices.py` | 对两份精确哈希产物生成价格常量补丁，保留各服基线差异 |

失效的分服源码快照生成脚本和针对单个UID/历史发奖批次的临时脚本已移除。当前产物基线和受限价格补丁的用途见[生产状态](../docs/production.md)。

Python凭据由环境变量或运行时输入提供，禁止写入源码。普通静态检查可以解析Python语法，但不能通过导入或直接运行会修改数据库/角色的维护脚本来做检查。
