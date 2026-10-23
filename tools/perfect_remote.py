"""把云服上已有的角色直接改成完美账号（满级、全护法满神通、满装备 / 法术书、元宝银两）。

用法：
    python perfect_remote.py 20005            # 按角色 uid（纯数字的账号名也行：不是 uid 就按账号名找）
    python perfect_remote.py test2 test3      # 按账号名（可一次给多个，uid 和账号名可混用）

在线的角色会先被踢下线。注意：护法 / 装备 / 法术书会整体换成完美配置，原有的不保留；闯关进度等其余数据保留。

连接：SSH 到云服后调本机管理接口 POST 127.0.0.1:28418/perfect_uid（管理接口只监听服务器本机）。
密码不写在脚本里：读环境变量 XYM_SSH_PWD，没有就运行时输入。主机可用 XYM_SSH_HOST 覆盖。
依赖：pip install paramiko
"""
import getpass
import json
import os
import sys

import paramiko

HOST = os.environ.get("XYM_SSH_HOST", "121.37.152.248")
ADMIN = "http://127.0.0.1:28418/perfect_uid"


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    targets = sys.argv[1:]
    if not targets:
        print(__doc__)
        sys.exit(1)
    pwd = os.environ.get("XYM_SSH_PWD") or getpass.getpass(f"root@{HOST} 密码: ")
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(HOST, username="root", password=pwd, timeout=20)
    ok = True
    try:
        for t in targets:
            # 纯数字既可能是 uid 也可能是账号名：先当 uid，没有这个角色再按账号名找
            body = {"uid": int(t), "urs": t} if t.isdigit() else {"urs": t}
            # 请求体走 stdin，避免账号名里的引号 / 中文被 shell 处理
            stdin, stdout, stderr = c.exec_command(
                f"curl -s -X POST {ADMIN} -H 'Content-Type: application/json' --data-binary @-", timeout=60)
            stdin.write(json.dumps(body, ensure_ascii=False).encode("utf-8"))
            stdin.channel.shutdown_write()
            out = stdout.read().decode("utf-8", "replace").strip()
            err = stderr.read().decode("utf-8", "replace").strip()
            try:
                res = json.loads(out)
            except ValueError:
                res = {"error": out or err or "no response"}
            if "uid" in res:
                print(f"{t}: 完成，uid {res['uid']}")
            else:
                ok = False
                print(f"{t}: 失败 - {res.get('error')}")
    finally:
        c.close()
    sys.exit(0 if ok else 2)


if __name__ == "__main__":
    main()
