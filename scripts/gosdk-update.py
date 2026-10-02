#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""gosdk-update —— 检查/更新 sandbox-tools 的预编译静态二进制（iSH / Linux）

用法：
    gosdk-update check     # 只看有没有新版本
    gosdk-update update    # 下载并安装最新版（自动校验 SHA256）
    gosdk-update auto      # 有更新才装（适合定时任务/登录时调用）

仓库：https://github.com/otaku-say/sandbox-tools （Release tag: latest）
"""
import hashlib
import json
import os
import platform
import shutil
import subprocess
import sys
import tempfile
import urllib.request

REPO = "otaku-say/sandbox-tools"
API = f"https://api.github.com/repos/{REPO}/releases/tags/latest"
DL = f"https://github.com/{REPO}/releases/download/latest"
STATE_DIR = os.path.expanduser("~/.gosdk-tools")
STATE = os.path.join(STATE_DIR, "state.json")
BIN_DIR = "/usr/local/bin"

TOOLS = ["cubesandbox-sdk-go", "sandbox-sdk-go"]
ARCH = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}


def arch():
    m = platform.machine().lower()
    a = ARCH.get(m)
    if not a:
        sys.exit(f"不支持的架构: {m}")
    return a


def load_state():
    try:
        with open(STATE) as f:
            return json.load(f)
    except Exception:
        return {}


def save_state(d):
    os.makedirs(STATE_DIR, exist_ok=True)
    with open(STATE, "w") as f:
        json.dump(d, f, indent=2)


def fetch(url, timeout=30):
    req = urllib.request.Request(url, headers={"User-Agent": "gosdk-update"})
    tok = os.environ.get("GITHUB_TOKEN")
    if tok and "api.github.com" in url:
        req.add_header("Authorization", f"Bearer {tok}")
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


def release_info():
    return json.loads(fetch(API))


def check(verbose=True):
    st = load_state()
    try:
        rel = release_info()
    except Exception as e:
        print(f"检查失败: {e}")
        return None
    pub = rel.get("published_at") or ""
    tag = rel.get("tag_name", "latest")
    assets = {a["name"]: a for a in rel.get("assets", [])}
    if not assets:
        print("Release 存在但没有资产（构建可能仍在进行）")
        return None
    installed = {t: os.path.exists(os.path.join(BIN_DIR, t)) for t in TOOLS}
    have_new = pub != st.get("published_at")
    if verbose:
        print(f"Release      : {tag}")
        print(f"发布时间     : {pub}  (本地记录: {st.get('published_at') or '无'})")
        print(f"资产         : {', '.join(sorted(assets))}")
        print(f"本机已安装   : {', '.join(t for t in TOOLS if installed[t]) or '无'}")
        print("→ " + ("发现新版本，可执行 gosdk-update update" if have_new else "已是最新"))
    return rel if have_new else None


def download(url, dest):
    data = fetch(url, timeout=120)
    with open(dest, "wb") as f:
        f.write(data)
    return data


def update():
    rel = check(verbose=True)
    if not rel:
        return 0
    a = arch()
    pub = rel.get("published_at") or ""
    tmp = tempfile.mkdtemp(prefix="gosdk-")
    sums = {}
    try:
        # 校验和清单
        try:
            txt = fetch(f"{DL}/SHA256SUMS", timeout=60).decode()
            for line in txt.splitlines():
                parts = line.split()
                if len(parts) == 2:
                    sums[parts[1].lstrip("*")] = parts[0]
        except Exception as e:
            print(f"警告：取 SHA256SUMS 失败（{e}），跳过校验")
        installed_ok = []
        for t in TOOLS:
            name = f"{t}-linux-{a}"
            if name not in rel.get("assets_by_name", {}) and not any(
                x["name"] == name for x in rel.get("assets", [])
            ):
                print(f"跳过 {name}（Release 中没有该资产）")
                continue
            path = os.path.join(tmp, name)
            print(f"下载 {name} ...", end=" ", flush=True)
            data = download(f"{DL}/{name}", path)
            got = hashlib.sha256(data).hexdigest()
            want = sums.get(name)
            if want and want != got:
                print("校验失败！")
                print(f"  期望 {want}\n  实际 {got}")
                return 1
            print(f"{len(data)//1024} KB" + (" (校验通过)" if want else ""))
            target = os.path.join(BIN_DIR, t)
            bak = target + ".bak"
            if os.path.exists(target):
                shutil.copy2(target, bak)
            os.chmod(path, 0o755)
            shutil.copy2(path, target)
            installed_ok.append(t)
        if installed_ok:
            st = load_state()
            st["published_at"] = pub
            st["updated_at"] = __import__("datetime").datetime.now().isoformat(timespec="seconds")
            st["installed"] = installed_ok
            save_state(st)
            print("已安装: " + ", ".join(installed_ok))
            for t in installed_ok:
                try:
                    out = subprocess.run([os.path.join(BIN_DIR, t), "version"],
                                         capture_output=True, text=True, timeout=20)
                    print("  " + (out.stdout or out.stderr).strip().splitlines()[0])
                except Exception:
                    pass
        return 0
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "check"
    if cmd == "check":
        check()
    elif cmd == "update":
        sys.exit(update())
    elif cmd == "auto":
        rel = check(verbose=False)
        if rel:
            print("发现新版本，开始更新 ...")
            sys.exit(update())
        print("已是最新，无需更新")
    else:
        print(__doc__)
        sys.exit(2)


if __name__ == "__main__":
    main()
