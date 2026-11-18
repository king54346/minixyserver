"""云服的 SSH 小工具（部署、查日志、取战报）。

用法：
    python ssh.py put <本地文件> <远端路径>     # SFTP 上传
    python ssh.py get <远端路径> <本地文件>     # SFTP 下载
    python ssh.py run '<shell 命令>'           # 执行并打印输出（退出码透传）

连接：主机读环境变量 DH（默认 121.37.152.248），root 密码读 DP（没有就运行时输入）——密码不写进任何文件。
Git Bash 下传远端绝对路径要 export MSYS_NO_PATHCONV=1。依赖：pip install paramiko
"""
import getpass
import os
import sys

import paramiko


def connect():
    host = os.environ.get("DH", "121.37.152.248")
    pwd = os.environ.get("DP") or getpass.getpass(f"root@{host} 密码: ")
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username="root", password=pwd, timeout=20)
    return c


def main():
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    if len(sys.argv) < 3 or sys.argv[1] not in ("put", "get", "run"):
        print(__doc__)
        sys.exit(1)
    c = connect()
    try:
        op = sys.argv[1]
        if op in ("put", "get"):
            sftp = c.open_sftp()
            if op == "put":
                sftp.put(sys.argv[2], sys.argv[3])
            else:
                sftp.get(sys.argv[2], sys.argv[3])
            sftp.close()
            print(op, sys.argv[3])
            return
        _, stdout, stderr = c.exec_command(sys.argv[2], timeout=600)
        out = stdout.read().decode("utf-8", "replace")
        err = stderr.read().decode("utf-8", "replace")
        sys.stdout.write(out)
        if err:
            sys.stderr.write(err)
        sys.exit(stdout.channel.recv_exit_status())
    finally:
        c.close()


if __name__ == "__main__":
    main()
