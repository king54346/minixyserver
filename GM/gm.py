"""小妖怪 GM 工具（本地网页版）。

运行：双击「启动GM工具.bat」，或
    python gm.py                                   # 直连模式：本机已用 SSH 隧道把服务器 127.0.0.1:28418 映射到本地 28418
    python gm.py --tunnel                         # 隧道模式：页面上输入服务器 root 密码，由本工具经 SSH 在服务器上 curl
    python gm.py --help                           # 看全部参数
然后浏览器打开 http://127.0.0.1:8765

原理：本机起一个只监听 127.0.0.1 的小网页服务。页面上的操作有两种转发方式：
  - 直连模式（默认）：本机直接 HTTP 请求 --admin（默认 http://127.0.0.1:28418，即 SSH 隧道出口）。
  - 隧道模式（--tunnel）：经 SSH 登录服务器，在服务器上用 curl 调游戏服的本机管理接口
    （127.0.0.1:28418，见 server/internal/xymsrv/admin.go、gm.go）。密码只保存在本进程内存里，不写盘。

配置优先级：命令行参数 > 环境变量 > 同目录 gm.conf > 内置默认值。
依赖：直连模式只需 Python 标准库；隧道模式另需 pip install paramiko
"""
import argparse
import json
import os
import shlex
import sys
import threading
import webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs
from urllib.request import Request, urlopen
from urllib.error import URLError, HTTPError

HERE = os.path.dirname(os.path.abspath(__file__))
CONF_FILE = os.path.join(HERE, "gm.conf")

# 内置默认值：适配本部署（minixyserver @ 你的服务器地址）。
# ADMIN 是“用户本机”上的管理接口地址：直连模式直接请求它（通常是 SSH 隧道本地端）；
# 隧道模式下它是在服务器上被 curl 的地址，保持 127.0.0.1 即可。
DEFAULTS = {
    "admin": "",                           # 未指定时按 SSH 主机选管理端口
    "host": "117.72.217.131",             # 隧道模式的 SSH 主机：环境变量 XYM_SSH_HOST / 参数 --ssh-host
    "user": "root",                      # 环境变量 XYM_SSH_USER / 参数 --ssh-user
    "port": "22",                        # 隧道模式的 SSH 端口：环境变量 XYM_SSH_PORT / 参数 --ssh-port
    "web_port": "8765",                  # 本机网页端口：环境变量 GM_PORT / 参数 --port
    "mode": "tunnel",                    # direct | tunnel：环境变量 XYM_MODE / 参数 --tunnel
}
ENV_KEYS = {"admin": "XYM_ADMIN", "host": "XYM_SSH_HOST", "user": "XYM_SSH_USER",
            "port": "XYM_SSH_PORT", "web_port": "GM_PORT", "mode": "XYM_MODE"}

def default_admin(host):
    return "http://127.0.0.1:" + ("30988" if host == "117.72.217.131" else "28418")


CFG = dict(DEFAULTS)
HOST = CFG["host"]
ADMIN = default_admin(HOST)
PORT = int(CFG["web_port"])
MODE = CFG["mode"]

# 静态页面白名单：请求路径 -> HERE 下的文件名（只从本目录读这几个固定文件，
# 不接受用户给定的路径，天然防目录遍历）。主页面顶部按钮新窗口打开后两个。
STATIC_PAGES = {
    "/game-rules.html": "game-rules.html",
    "/records.html": "records.html",
    "/box.html": "box.html",
    "/": "index.html",
    "/index.html": "index.html",
    "/accounts.html": "accounts.html",   # 账号注册 / 管理（列表、重置密码、删除）
    "/pets.html": "pets.html",           # 护法培养
    "/dongfu.html": "dongfu.html",       # 洞府六座建筑等级
    "/xiuwei.html": "xiuwei.html",       # 角色个人修炼
    "/progress.html": "progress.html",   # 角色关卡进度
    "/bag.html": "bag.html",             # 物品栏（道具 / 装备 / 法术书）
    "/operations.html": "operations.html",
    "/broadcast.html": "broadcast.html",  # 全服播报（浮动提示 + 世界频道）
}

_ssh = None
_lock = threading.Lock()


def load_conf_file():
    """读同目录 gm.conf：每行 key=value，# 开头是注释。缺省/写错不影响启动。"""
    if not os.path.isfile(CONF_FILE):
        return
    try:
        with open(CONF_FILE, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, _, v = line.partition("=")
                k, v = k.strip(), v.strip().strip('"').strip("'")
                if k in DEFAULTS and v:
                    CFG[k] = v
    except OSError as e:
        print(f"[警告] 读 {CONF_FILE} 失败：{e}")


def parse_args(argv=None):
    p = argparse.ArgumentParser(
        description="小妖怪 GM 工具（本地网页版）。默认直连 http://127.0.0.1:28418（先用 SSH 隧道把服务器管理接口映射到本地）。",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""示例：
  python gm.py                                     # 直连模式，管理接口 http://127.0.0.1:28418
  python gm.py --admin http://127.0.0.1:28418       # 直连模式，指定管理接口地址
  python gm.py --tunnel                             # 隧道模式（需要 paramiko），页面输入 root 密码
  python gm.py --tunnel --ssh-host 你的服务器地址 --ssh-port 22

配置文件 gm.conf（与 gm.py 同目录）示例：
  admin = http://127.0.0.1:28418
  host = 你的服务器地址
  mode = direct

只做连通性自检（不需要开网页，仅查询类接口）：
  python gm.py --selftest
""")
    p.add_argument("--admin", help="管理接口地址（默认 http://127.0.0.1:28418，环境变量 XYM_ADMIN）")
    p.add_argument("--tunnel", action="store_true", help="隧道模式：经 SSH 在服务器上 curl 管理接口（需要 paramiko）")
    p.add_argument("--direct", action="store_true", help="直连模式（默认）：本机直接请求 --admin")
    p.add_argument("--ssh-host", help="隧道模式的服务器地址（默认 你的服务器地址，环境变量 XYM_SSH_HOST）")
    p.add_argument("--ssh-user", help="隧道模式的 SSH 用户（默认 root，环境变量 XYM_SSH_USER）")
    p.add_argument("--ssh-port", help="隧道模式的 SSH 端口（默认 22，环境变量 XYM_SSH_PORT）")
    p.add_argument("--port", help="本机网页端口（默认 8765，环境变量 GM_PORT）")
    p.add_argument("--no-browser", action="store_true", help="启动后不自动打开浏览器")
    p.add_argument("--selftest", action="store_true", help="只调管理接口自检并打印结果，不启动网页服务")
    return p.parse_args(argv)


def apply_config(args):
    """按 默认值 < gm.conf < 环境变量 < 命令行 的顺序合并配置。"""
    global CFG, HOST, ADMIN, PORT, MODE
    CFG = dict(DEFAULTS)
    load_conf_file()
    if os.environ.get("XYM_ADMIN_PORT"):
        CFG["admin"] = "http://127.0.0.1:" + str(int(os.environ["XYM_ADMIN_PORT"]))
    for k, env in ENV_KEYS.items():
        if os.environ.get(env):
            CFG[k] = os.environ[env]
    if args.admin:
        CFG["admin"] = args.admin
    if args.ssh_host:
        CFG["host"] = args.ssh_host
    if args.ssh_user:
        CFG["user"] = args.ssh_user
    if args.ssh_port:
        CFG["port"] = args.ssh_port
    if args.port:
        CFG["web_port"] = args.port
    if args.tunnel:
        CFG["mode"] = "tunnel"
    if args.direct:
        CFG["mode"] = "direct"
    if not CFG["admin"]:
        CFG["admin"] = default_admin(CFG["host"])
    # 兼容手写配置：允许 admin 写成 127.0.0.1:28418（没有 http://）
    if "://" not in CFG["admin"]:
        CFG["admin"] = "http://" + CFG["admin"]
    CFG["admin"] = CFG["admin"].rstrip("/")
    CFG["mode"] = "tunnel" if str(CFG["mode"]).strip().lower() in ("tunnel", "ssh") else "direct"
    HOST, ADMIN, MODE = CFG["host"], CFG["admin"], CFG["mode"]
    try:
        PORT = int(CFG["web_port"])
    except ValueError:
        PORT = 8765


def connect(pwd):
    """隧道模式：SSH 登录服务器（paramiko 只在用到时才导入，直连模式无需安装）。"""
    global _ssh
    import paramiko  # 延迟导入，直连模式不依赖

    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        c.connect(HOST, port=int(CFG["port"]), username=CFG["user"], password=pwd, timeout=20)
        code, data = ssh_request(c, "GET", "/players")
        if code != 200 or not isinstance(data, list):
            raise RuntimeError(f"SSH 已通，但管理接口 {ADMIN} 不可用：{data}")
    except Exception:
        c.close()
        raise
    with _lock:
        if _ssh:
            _ssh.close()
        _ssh = c


def admin_local(method, path, body=None):
    """直连模式：本机直接请求管理接口，返回 (http 状态码, 解析后的 JSON 或文本)。"""
    url = ADMIN + path
    data = None
    headers = {"Content-Type": "application/json"}
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
    req = Request(url, data=data, headers=headers, method=method)
    try:
        with urlopen(req, timeout=60) as r:
            raw, code = r.read().decode("utf-8", "replace"), r.status
    except HTTPError as e:  # 4xx/5xx 也带 JSON 错误体
        raw, code = e.read().decode("utf-8", "replace"), e.code
    except URLError as e:
        return 502, {"error": f"连不上管理接口 {url}：{e.reason}（隧道没建好？游戏服没启动？）"}
    except OSError as e:
        return 502, {"error": f"连不上管理接口 {url}：{e}"}
    try:
        return code, json.loads(raw) if raw.strip() else {}
    except ValueError:
        return code, {"error": raw}


def admin_ssh(method, path, body=None):
    """隧道模式：在服务器上 curl 管理接口，返回 (http 状态码, 解析后的 JSON 或文本)。"""
    with _lock:
        c = _ssh
    if c is None:
        return 401, {"error": "还没连接服务器"}
    t = c.get_transport()
    if t is None or not t.is_active():
        return 401, {"error": "SSH 连接已断开，请重新连接"}
    try:
        return ssh_request(c, method, path, body)
    except Exception as e:
        return 502, {"error": f"SSH 管理请求失败：{e}"}


def ssh_request(c, method, path, body=None):
    cmd = f"curl --noproxy '*' -sS --connect-timeout 5 --max-time 55 -X {method} {shlex.quote(ADMIN + path)} -H 'Content-Type: application/json' -w '\\n%{{http_code}}'"
    if body is not None:
        cmd += " --data-binary @-"
    stdin, stdout, stderr = c.exec_command(cmd, timeout=60)
    if body is not None:
        stdin.write(json.dumps(body, ensure_ascii=False).encode("utf-8"))
    stdin.channel.shutdown_write()
    out = stdout.read().decode("utf-8", "replace")
    text, _, code = out.rpartition("\n")
    if not code.isdigit() or not 100 <= int(code) <= 599:
        detail = stderr.read().decode("utf-8", "replace").strip()
        return 502, {"error": f"管理接口 {ADMIN} 无响应：{detail or out.strip()}"}
    code = int(code)
    try:
        return code, json.loads(text) if text.strip() else {}
    except ValueError:
        return code, {"error": text}


def admin(method, path, body=None):
    """按模式分发：直连 or 经 SSH。"""
    return admin_ssh(method, path, body) if MODE == "tunnel" else admin_local(method, path, body)


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
        if u.path == "/api/registration":
            return self.send(*admin("GET", "/gm/registration"))
        if u.path == "/api/game-rules":
            from urllib.parse import urlencode
            q = parse_qs(u.query)
            allowed = {k: q[k][0] for k in ("group", "q", "offset") if k in q}
            return self.send(*admin("GET", "/gm/game-rules?" + urlencode(allowed)))
        if u.path in STATIC_PAGES:
            with open(os.path.join(HERE, STATIC_PAGES[u.path]), "rb") as f:
                return self.send(200, f.read(), "text/html; charset=utf-8")
        if u.path == "/api/box":
            return self.send(*admin("GET", "/box"))
        if u.path in ("/api/records/player", "/api/records/detail"):
            from urllib.parse import urlencode
            q = parse_qs(u.query)
            allowed = {k: q[k][0] for k in ("uid", "day", "offset", "f") if k in q}
            return self.send(*admin("GET", u.path[4:] + "?" + urlencode(allowed)))
        if u.path == "/api/operations":
            return self.send(*admin("GET", "/gm/operations"))
        if u.path == "/api/logs":
            from urllib.parse import urlencode
            q = parse_qs(u.query)
            allowed = {k: q[k][0] for k in ("date", "uid", "kind", "limit") if k in q}
            return self.send(*admin("GET", "/gm/logs?" + urlencode(allowed)))
        if u.path == "/api/chat":
            return self.send(*admin("GET", "/chat"))
        if u.path == "/api/players":
            return self.send(*admin("GET", "/players"))
        if u.path == "/api/catalog":
            return self.send(*admin("GET", "/catalog"))
        if u.path == "/api/player":
            uid = parse_qs(u.query).get("uid", ["0"])[0]
            return self.send(*admin("GET", "/player?uid=" + str(int(uid or 0))))
        if u.path == "/api/pets":
            uid = parse_qs(u.query).get("uid", ["0"])[0]
            return self.send(*admin("GET", "/pets?uid=" + str(int(uid or 0))))
        if u.path == "/api/accounts":
            return self.send(*admin("GET", "/accounts"))
        if u.path == "/api/dongfus":
            return self.send(*admin("GET", "/dongfus"))
        if u.path == "/api/xiuwei":
            uid = parse_qs(u.query).get("uid", ["0"])[0]
            return self.send(*admin("GET", "/xiuwei?uid=" + str(int(uid or 0))))
        if u.path == "/api/progress":
            uid = parse_qs(u.query).get("uid", ["0"])[0]
            return self.send(*admin("GET", "/progress?uid=" + str(int(uid or 0))))
        if u.path == "/api/bag":
            uid = parse_qs(u.query).get("uid", ["0"])[0]
            return self.send(*admin("GET", "/bag?uid=" + str(int(uid or 0))))
        if u.path == "/api/status":
            if MODE == "tunnel":
                with _lock:
                    c = _ssh
                    ok = bool(c is not None and c.get_transport() is not None and c.get_transport().is_active())
            else:
                code, _ = admin_local("GET", "/players")
                ok = code == 200
            return self.send(200, {"connected": ok, "host": HOST, "mode": MODE, "admin": ADMIN})
        self.send(404, {"error": "not found"})

    def do_POST(self):
        origin = self.headers.get("Origin")
        if origin and origin not in (f"http://127.0.0.1:{PORT}", f"http://localhost:{PORT}"):
            return self.send(403, {"error": "invalid origin"})
        u = urlparse(self.path)
        try:
            b = self.body()
        except ValueError:
            return self.send(400, {"error": "bad json"})
        if u.path == "/api/connect":
            if MODE != "tunnel":
                code, _ = admin_local("GET", "/players")
                if code == 200:
                    return self.send(200, {"ok": True, "host": ADMIN, "mode": MODE})
                return self.send(401, {"error": f"直连模式连不上 {ADMIN}，请检查 SSH 隧道 / 游戏服是否在运行"})
            try:
                connect(b.get("password", ""))
            except Exception as e:  # noqa: BLE001 — 把原因显示给页面
                return self.send(401, {"error": f"连接失败：{e}"})
            return self.send(200, {"ok": True, "host": HOST, "mode": MODE})
        routes = {"/api/settings": "/gm/settings", "/api/preview": "/gm/preview", "/api/run": "/gm/run", "/api/mail": "/mail", "/api/role": "/role", "/api/perfect": "/perfect_uid", "/api/kick": "/kick",
                  "/api/pet_point": "/pet_point", "/api/pet_relate": "/pet_relate", "/api/account": "/account",
                  "/api/account_pwd": "/account_pwd", "/api/account_delete": "/account_delete",
                  "/api/dongfu_build": "/dongfu_build", "/api/dongfu_lv": "/dongfu_lv",
                  "/api/xiuwei_set": "/xiuwei_set", "/api/progress_set": "/progress_set",
                  "/api/bag_add": "/bag_add", "/api/bag_del": "/bag_del",
                  "/api/bag_suit": "/suit_all",
                  "/api/broadcast": "/broadcast", "/api/box": "/box_set",
                  "/api/box_reset": "/box_reset"}
        routes["/api/game-rules"] = "/gm/game-rules"
        routes["/api/registration"] = "/gm/registration"
        if u.path in routes:
            return self.send(*admin("POST", routes[u.path], b))
        self.send(404, {"error": "not found"})


def selftest():
    """不开网页，直接调管理接口自检（只做查询，无副作用）。"""
    sys.stdout.reconfigure(encoding="utf-8")
    print(f"模式: {MODE}   管理接口: {ADMIN}")
    if MODE == "tunnel":
        print("隧道模式自检需要输入 SSH 密码，请用网页连接，或改用直连模式 --selftest")
        return 1
    ok = True
    code, data = admin_local("GET", "/players")
    print(f"GET /players -> HTTP {code}  {json.dumps(data, ensure_ascii=False)[:300]}")
    ok = ok and code == 200
    code, data = admin_local("GET", "/catalog")
    print(f"GET /catalog -> HTTP {code}  条目数 {len(data) if isinstance(data, list) else '-'}")
    ok = ok and code == 200
    code, data = admin_local("GET", "/player?uid=1")
    print(f"GET /player?uid=1 -> HTTP {code}  {json.dumps(data, ensure_ascii=False)[:200]}")
    print("自检通过（查询接口均可用）" if ok else "自检未通过（见上）")
    return 0 if ok else 2


def main():
    args = parse_args()
    apply_config(args)
    sys.stdout.reconfigure(encoding="utf-8")
    if args.selftest:
        sys.exit(selftest())
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    url = f"http://127.0.0.1:{PORT}"
    tip = (f"隧道模式，服务器 {CFG['user']}@{HOST}:{CFG['port']}" if MODE == "tunnel"
           else f"直连模式，管理接口 {ADMIN}")
    print(f"GM 工具已启动：{url}（{tip}；关掉这个窗口即退出）")
    if not args.no_browser:
        threading.Timer(0.5, lambda: webbrowser.open(url)).start()
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
