"""小妖怪 GM 工具（本地网页版）。

运行：双击「启动GM工具.bat」，或 python gm.py，然后浏览器打开 http://127.0.0.1:8765
页面里输入云服 root 密码连接（密码只保存在本进程内存里，不写盘）。

原理：本机起一个只监听 127.0.0.1 的小网页服务；页面的操作经 SSH 转到云服，在服务器上用 curl 调游戏服的
本机管理接口（127.0.0.1:28418，见 server/internal/xymsrv/admin.go、gm.go）。
依赖：pip install paramiko
"""
import json
import os
import shlex
import sys
import threading
import webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

import paramiko

HOST = os.environ.get("XYM_SSH_HOST", "121.37.152.248")
ADMIN = "http://127.0.0.1:28418"
PORT = int(os.environ.get("GM_PORT", "8765"))
HERE = os.path.dirname(os.path.abspath(__file__))

_ssh = None
_lock = threading.Lock()


def connect(pwd):
    global _ssh
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(HOST, username="root", password=pwd, timeout=20)
    with _lock:
        if _ssh:
            _ssh.close()
        _ssh = c


def admin(method, path, body=None):
    """在服务器上 curl 管理接口，返回 (http 状态码, 解析后的 JSON 或文本)。"""
    with _lock:
        c = _ssh
    if c is None:
        return 401, {"error": "还没连接服务器"}
    t = c.get_transport()
    if t is None or not t.is_active():
        return 401, {"error": "SSH 连接已断开，请重新连接"}
    cmd = f"curl -s -X {method} {shlex.quote(ADMIN + path)} -H 'Content-Type: application/json' -w '\\n%{{http_code}}'"
    if body is not None:
        cmd += " --data-binary @-"
    stdin, stdout, stderr = c.exec_command(cmd, timeout=60)
    if body is not None:
        stdin.write(json.dumps(body, ensure_ascii=False).encode("utf-8"))
    stdin.channel.shutdown_write()
    out = stdout.read().decode("utf-8", "replace")
    text, _, code = out.rpartition("\n")
    try:
        code = int(code)
    except ValueError:
        return 502, {"error": out or stderr.read().decode("utf-8", "replace") or "无响应（游戏服没启动？）"}
    try:
        return code, json.loads(text) if text.strip() else {}
    except ValueError:
        return code, {"error": text}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def send(self, code, obj, ctype="application/json; charset=utf-8"):
        data = obj if isinstance(obj, bytes) else json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(n) or b"{}")

    def do_GET(self):
        u = urlparse(self.path)
        if u.path in ("/", "/index.html"):
            with open(os.path.join(HERE, "index.html"), "rb") as f:
                return self.send(200, f.read(), "text/html; charset=utf-8")
        if u.path == "/api/players":
            return self.send(*admin("GET", "/players"))
        if u.path == "/api/catalog":
            return self.send(*admin("GET", "/catalog"))
        if u.path == "/api/player":
            uid = parse_qs(u.query).get("uid", ["0"])[0]
            return self.send(*admin("GET", "/player?uid=" + str(int(uid or 0))))
        if u.path == "/api/status":
            with _lock:
                ok = _ssh is not None and _ssh.get_transport() is not None and _ssh.get_transport().is_active()
            return self.send(200, {"connected": ok, "host": HOST})
        self.send(404, {"error": "not found"})

    def do_POST(self):
        u = urlparse(self.path)
        try:
            b = self.body()
        except ValueError:
            return self.send(400, {"error": "bad json"})
        if u.path == "/api/connect":
            try:
                connect(b.get("password", ""))
            except Exception as e:  # noqa: BLE001 — 把原因显示给页面
                return self.send(401, {"error": f"连接失败：{e}"})
            return self.send(200, {"ok": True, "host": HOST})
        routes = {"/api/mail": "/mail", "/api/role": "/role", "/api/perfect": "/perfect_uid", "/api/kick": "/kick"}
        if u.path in routes:
            return self.send(*admin("POST", routes[u.path], b))
        self.send(404, {"error": "not found"})


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    url = f"http://127.0.0.1:{PORT}"
    print(f"GM 工具已启动：{url}（关掉这个窗口即退出）")
    threading.Timer(0.5, lambda: webbrowser.open(url)).start()
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
