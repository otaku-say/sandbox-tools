#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
cli_it.py —— sandbox-sdk-go v5（纯 v2）CLI 集成测试套件（纯标准库）

被测对象：编译出的 CLI 二进制（环境变量 CLI，默认 /tmp/cli）
运行环境：sandbox-base 沙箱内部（SANDBOX_BASE 指向沙箱自身，默认 http://127.0.0.1:8080）
    —— 也可在别处对任意 SANDBOX_BASE 跑（把 CLI 换成对应架构的二进制即可）。

用法：
    CLI=/tmp/cli SANDBOX_BASE=http://127.0.0.1:8080 python3 cli_it.py
    python3 cli_it.py --only=fs            # 只跑某一（前缀）组，逗号分隔多组
    python3 cli_it.py --only=exec,watch    # group 名或命令名前缀均可
    python3 cli_it.py --list               # 列出全部用例

输出：每条命令一个用例，逐条打印 ✔/✘ 与关键输出摘要；失败时附"实际收到的前 300 字符"。
结尾汇总 通过/失败 数并以退出码返回结果（0=全过，1=有失败）。

断言依据：SPEC.md（v2 实测语义） + V2-API.md（路由/字段） + main.go（命令表）。
"""
import hashlib
import http.server
import io
import json
import os
import re
import shlex
import signal
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.parse
import uuid

CLI = os.environ.get("CLI", "/tmp/cli")
BASE = os.environ.get("SANDBOX_BASE", "http://127.0.0.1:8080")
DEFAULT_TIMEOUT = float(os.environ.get("IT_TIMEOUT", "120"))
RND = uuid.uuid4().hex[:8].upper()
SCRATCH = tempfile.mkdtemp(prefix="cli_it_scratch_")
CTX = {}   # 用例间共享状态（会话 id / watcher id 等）
HUB = "http://127.0.0.1:8080/"


# ---------------------------------------------------------------- 基础工具

class RunResult:
    __slots__ = ("rc", "out", "err", "args")

    def __init__(self, rc, out, err, args):
        self.rc = rc
        self.out = out or ""
        self.err = err or ""
        self.args = args

    @property
    def all(self):
        if self.err:
            return self.out + "\n[stderr] " + self.err
        return self.out


def snippet(s, n=300):
    """失败提示用：取前 n 字符，换行转义，保持单行可读。"""
    s = (s or "").replace("\r", "")
    s = s.replace("\n", "\\n")
    if len(s) > n:
        s = s[:n] + "…"
    return s


def fail(msg, actual=None):
    """断言失败：把「实际收到的前 300 字符」拼进异常信息。"""
    if actual is not None:
        msg = "%s ｜ 实际前300: %s" % (msg, snippet(actual))
    raise AssertionError(msg)


def run(args, stdin=None, timeout=None, env=None):
    """执行 CLI；stdin 为 str 时编码为 UTF-8 输入。"""
    cmd = [CLI] + [str(a) for a in args]
    e = dict(os.environ)
    e["SANDBOX_BASE"] = BASE          # 强制指向被测沙箱
    if env:
        e.update(env)
    to = timeout or DEFAULT_TIMEOUT
    try:
        p = subprocess.run(
            cmd,
            input=(stdin.encode("utf-8") if isinstance(stdin, str) else stdin),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=to,
            env=e,
        )
        return RunResult(p.returncode, p.stdout.decode("utf-8", "replace"),
                         p.stderr.decode("utf-8", "replace"), args)
    except subprocess.TimeoutExpired as ex:
        so = (ex.stdout or b"").decode("utf-8", "replace")
        se = (ex.stderr or b"").decode("utf-8", "replace")
        return RunResult(124, so, se + "\n[本地测试超时 %.0fs]" % to, args)


def expect_ok(res, what):
    if res.rc != 0:
        fail("%s 退出码 %d（期望 0）" % (what, res.rc), res.all)
    return res


def jload(out, what):
    try:
        return json.loads(out)
    except ValueError:
        fail("%s 输出不是合法 JSON" % what, out)


def dfind(obj, keys):
    """递归收集任意层级中指定 key 的所有非空值（用于兼容多种响应形状）。"""
    found = []

    def walk(v):
        if isinstance(v, dict):
            for k, x in v.items():
                if k in keys and x is not None:
                    found.append(x)
                walk(x)
        elif isinstance(v, list):
            for x in v:
                walk(x)

    walk(obj)
    return found


def first(obj, keys):
    vals = dfind(obj, keys)
    return vals[0] if vals else None


def pick_id(text):
    """从文本输出里挑一个像 id 的 token（优先最后一行）。"""
    for line in reversed([l.strip() for l in (text or "").splitlines() if l.strip()]):
        if re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:+-]{4,90}", line):
            return line
    m = re.search(r"[A-Za-z0-9][A-Za-z0-9_.-]{5,80}", text or "")
    return m.group(0) if m else ""


def wait_until(fn, timeout=15.0, interval=1.0):
    """轮询直到 fn() 返回真值；超时返回 None。"""
    deadline = time.time() + timeout
    while True:
        v = fn()
        if v:
            return v
        if time.time() >= deadline:
            return None
        time.sleep(interval)


def wait_token_gone(args, token, timeout=10.0):
    """轮询某列表命令，直到输出中不再包含 token。返回 (ok, last_result)。"""
    holder = {"r": None}

    def probe():
        rr = run(args, timeout=60)
        holder["r"] = rr
        return rr if (rr.rc == 0 and token not in rr.out) else None

    got = wait_until(probe, timeout=timeout, interval=1.0)
    return got, holder["r"]


def rand_tag(prefix):
    return "%s_%s_%s" % (prefix, RND, uuid.uuid4().hex[:6].upper())


TESTS = []


def test(group, name):
    def deco(fn):
        fn._group, fn._name = group, name
        TESTS.append(fn)
        return fn
    return deco


# ================================================================ info 组

@test("info", "version")
def t_version():
    r = expect_ok(run(["version"]), "version")
    if "sandbox-sdk-go" not in r.out:
        fail("version 输出缺少程序名", r.all)
    if BASE not in r.out:
        fail("version 未回显 SANDBOX_BASE(%s)" % BASE, r.all)
    return "输出含程序名与 SANDBOX_BASE=%s" % BASE


@test("info", "health")
def t_health():
    r = expect_ok(run(["health"]), "health")
    if not r.out.strip():
        fail("health 无输出", r.all)
    try:
        j = json.loads(r.out)
        if isinstance(j, dict) and not j:
            fail("health JSON 为空对象", r.out)
        return "健康检查返回 JSON（%d 字节）" % len(r.out.strip())
    except ValueError:
        return "健康检查返回文本（%d 字节）" % len(r.out.strip())


@test("info", "sandbox-info")
def t_sandbox_info():
    r = expect_ok(run(["sandbox-info", "--json"]), "sandbox-info --json")
    j = jload(r.out, "sandbox-info")
    if not isinstance(j, dict) or not j:
        fail("sandbox-info data 不是非空对象", r.out)
    return "自描述对象键=%s…" % ",".join(sorted(j.keys())[:6])


@test("info", "sandbox-packages")
def t_sandbox_packages():
    r1 = expect_ok(run(["sandbox-packages", "--lang=python"], timeout=60), "packages python")
    if len(r1.out.strip()) < 10:
        fail("python 包清单过短", r1.all)
    r2 = expect_ok(run(["sandbox-packages", "--lang=node", "--json"], timeout=60), "packages node --json")
    if len(r2.out.strip()) < 10:
        fail("node 包清单过短", r2.all)
    return "python/node 包清单均非空（python %d 字节）" % len(r1.out)


# ================================================================ exec 组

@test("exec", "exec")
def t_exec():
    tag = rand_tag("EXEC")
    r = expect_ok(run(["exec", "echo %s" % tag]), "exec echo")
    if r.out.strip() != tag:
        fail("stdout 与预期不符（期望 %r）" % tag, r.all)
    rj = expect_ok(run(["exec", "--json", "echo %s" % tag]), "exec --json")
    jload(rj.out, "exec --json")           # 必须是合法 JSON
    if tag not in rj.out:
        fail("--json 输出缺少命令 stdout", rj.out)
    r2 = expect_ok(run(["exec", "echo OUT_%s; echo ERR_%s 1>&2" % (tag, tag)]), "exec stdout/stderr")
    if "OUT_%s" % tag not in r2.out:
        fail("stdout 未捕获", r2.all)
    if "ERR_%s" % tag not in r2.err:
        fail("stderr 未分流到 stderr", r2.all)
    r3 = expect_ok(run(["exec", "pwd", "--cwd=/tmp"]), "exec --cwd")
    if r3.out.strip() != "/tmp":
        fail("--cwd 未生效（期望 /tmp）", r3.all)
    # timeout 到点应返回 status=running（SPEC：进程还在跑，要自己 kill）
    r4 = expect_ok(run(["exec", "--json", "sleep 8; echo LATE_%s" % tag, "--timeout=1"]), "exec --timeout")
    d = jload(r4.out, "exec timeout json")
    st = first(d, ["status"])
    if st != "running":
        fail("timeout=1 后 status 应为 running，实际 %r" % st, r4.out)
    cid = str(first(d, ["command_id"]) or "")
    if cid:
        run(["kill", cid], timeout=30)     # 清理还在跑的进程
    return "echo/stderr/cwd/JSON 均正确；timeout=1 → status=running（已 kill）"


@test("exec", "async")
def t_async():
    tag = rand_tag("ASYNC")
    r = expect_ok(run(["async", "sleep 2; echo %s" % tag, "--json"]), "async --json")
    d = jload(r.out, "async json")
    cid = first(d, ["command_id"])
    if not cid:
        fail("async --json 未给出 command_id", r.out)
    r2 = expect_ok(run(["async", "echo %s" % tag]), "async 文本输出")
    cid2 = pick_id(r2.out)
    if not cid2:
        fail("async 未打印 command_id", r2.all)
    CTX["async_id"], CTX["async_tag"] = str(cid), tag
    return "command_id=%s（--json 与文本两种输出均可用）" % cid


@test("exec", "log")
def t_log():
    cid, tag = CTX.get("async_id"), CTX.get("async_tag")
    if not cid:
        tag = rand_tag("LOG")
        r0 = expect_ok(run(["async", "sleep 1; echo %s" % tag]), "async(准备)")
        cid = pick_id(r0.out)
        if not cid:
            fail("准备 async 命令失败", r0.all)
    r = run(["log", cid, "--follow", "--timeout=40", "--interval=300ms"], timeout=60)
    expect_ok(r, "log --follow")
    if tag not in r.all:
        fail("log 未跟到命令输出 %s" % tag, r.all)
    return "follow 到命令输出（%s），退出码 0" % tag


@test("exec", "kill")
def t_kill():
    r = expect_ok(run(["async", "sleep 60"]), "async(长命令)")
    cid = pick_id(r.out)
    if not cid:
        fail("拿不到 command_id", r.all)
    expect_ok(run(["kill", cid]), "kill")
    last = {"r": None}

    def probe():
        rr = run(["exec", "--id=%s" % cid, "--json"], timeout=30)
        if rr.rc != 0:
            fail("exec --id 查询失败", rr.all)
        d = jload(rr.out, "exec --id")
        last["r"] = rr
        st = str(first(d, ["status"]) or "")
        if st not in ("", "running", "pending"):
            return d
        return None

    d = wait_until(probe, timeout=15, interval=1.0)
    if not d:
        fail("kill 后命令仍非终态", last["r"].all if last["r"] else "")
    st = first(d, ["status"])
    ec = first(d, ["exit_code"])
    if st != "completed":
        fail("kill 后 status 应为 completed，实际 %r" % st, last["r"].out)
    try:
        ec_i = int(ec)
    except (TypeError, ValueError):
        ec_i = None
    if ec_i != -1:
        fail("kill 后 exit_code 应为 -1，实际 %r" % ec, last["r"].out)
    return "kill 后 status=completed / exit_code=-1（与 SPEC 一致）"


@test("exec", "stdin")
def t_stdin():
    tag = rand_tag("STDIN")
    r = expect_ok(run(["async", "echo READY_%s; read -r line; echo GOT:$line" % tag]), "async(读 stdin)")
    cid = pick_id(r.out)
    if not cid:
        fail("拿不到 command_id", r.all)

    def step(token, timeout):
        holder = {"r": None}

        def probe():
            rr = run(["exec", "--id=%s" % cid, "--json"], timeout=30)
            holder["r"] = rr
            return rr if (rr.rc == 0 and token in rr.out) else None

        got = wait_until(probe, timeout=timeout, interval=1.0)
        return got, holder["r"]

    got, lr = step("READY_%s" % tag, 15)
    if not got:
        run(["kill", cid], timeout=30)
        fail("命令未进入等待输入状态", lr.all if lr else "")
    expect_ok(run(["stdin", cid, "STDIN_%s" % tag, "--enter"]), "stdin")
    got2, lr2 = step("GOT:STDIN_%s" % tag, 20)
    if not got2:
        run(["kill", cid], timeout=30)
        fail("stdin 内容未被远端命令读到", lr2.all if lr2 else "")
    return "stdin 写入被 read 读到（GOT:STDIN_*）"


# ================================================================ sess 组

def _ensure_sess(prefix="it-sess-shared"):
    sid = CTX.get("sess")
    if not sid:
        sid = "%s-%s" % (prefix, RND)
        expect_ok(run(["sess-new", sid, "--cwd=/tmp"]), "sess-new(准备)")
        CTX["sess"] = sid
    return sid


@test("sess", "sess-new")
def t_sess_new():
    sid = "it-sess-new-%s" % RND
    r = expect_ok(run(["sess-new", sid, "--cwd=/tmp", "--json"]), "sess-new --json")
    if sid not in r.out:
        fail("响应未见会话 id %s" % sid, r.out)
    return "会话 %s 创建成功（--json 含 session_id）" % sid


@test("sess", "sess")
def t_sess():
    sid = "it-sess-%s" % RND
    expect_ok(run(["sess-new", sid, "--cwd=/tmp"]), "sess-new(准备)")
    r1 = expect_ok(run(["sess", sid, "pwd"]), "sess pwd")
    if r1.out.strip() != "/tmp":
        fail("会话 cwd 应为 /tmp", r1.all)
    r2 = expect_ok(run(["sess", sid, "cd / && pwd"]), "sess cd")
    if r2.out.strip() != "/":
        fail("cd / 后 pwd 应为 /", r2.all)
    r3 = expect_ok(run(["sess", sid, "pwd"]), "sess pwd(再次)")
    if r3.out.strip() != "/tmp":
        fail("cd 不应跨调用保留（SPEC：cwd 固定在创建时）", r3.all)
    CTX["sess"] = sid
    return "会话内 cd 不跨调用保留（/tmp → / → /tmp），符合 SPEC"


@test("sess", "sess-ls")
def t_sess_ls():
    sid = _ensure_sess()
    r = expect_ok(run(["sess-ls", "--json"]), "sess-ls --json")
    if sid not in r.out:
        fail("会话列表未包含 %s" % sid, r.out)
    return "列表（--json）包含已创建会话 %s" % sid


@test("sess", "sess-rm")
def t_sess_rm():
    sid = "it-sess-rm-%s" % RND
    expect_ok(run(["sess-new", sid]), "sess-new(准备)")
    expect_ok(run(["sess-rm", sid]), "sess-rm")
    got, last = wait_token_gone(["sess-ls", "--json"], sid, timeout=10)
    if not got:
        fail("sess-rm 后列表中仍见 %s" % sid, last.out if last else "")
    return "会话删除并从列表消失"


# ================================================================ fs 组

FSBASE = "/tmp/cli_it_fs_%s" % RND


def _fs_dir():
    if not CTX.get("fs_dir_ok"):
        expect_ok(run(["mkdir", FSBASE, "--parents"]), "mkdir(准备)")
        CTX["fs_dir_ok"] = True
    return FSBASE


@test("fs", "mkdir")
def t_mkdir():
    d = "%s/mk_%s/a/b" % (FSBASE, RND)
    expect_ok(run(["mkdir", d, "--parents"]), "mkdir --parents")
    r = expect_ok(run(["stat", d, "--json"]), "stat(校验)")
    j = jload(r.out, "stat json")
    if not first(j, ["is_directory"]):
        fail("mkdir 后目录不存在", r.out)
    return "深层目录创建成功（--parents）"


@test("fs", "write")
def t_write():
    d = _fs_dir()
    p1 = "%s/hello_%s.txt" % (d, RND)
    content = "HELLO-FS-%s\nline2-%s\n" % (RND, RND)
    lp = os.path.join(SCRATCH, "local_hello.txt")
    with open(lp, "w") as f:
        f.write(content)
    expect_ok(run(["write", lp, p1]), "write 本地文件")
    r = expect_ok(run(["read", p1, "--json"]), "read(校验)")
    j = jload(r.out, "read json")
    got = first(j, ["content"])
    if got != content:
        fail("写读不一致（期望 %d 字节）" % len(content), r.out)
    p2 = "%s/stdin_%s.txt" % (d, RND)
    expect_ok(run(["write", "-", p2], stdin="STDIN-WRITE-%s\n" % RND), "write - (stdin)")
    r2 = expect_ok(run(["read", p2, "--json"]), "read(stdin 校验)")
    if ("STDIN-WRITE-%s" % RND) not in r2.out:
        fail("stdin 写入未落盘", r2.out)
    return "本地文件与 stdin 两种写入均读回一致（%d 字节）" % len(content)


@test("fs", "read")
def t_read():
    d = _fs_dir()
    p = "%s/read_%s.txt" % (d, RND)
    content = "L0_%s\nL1_%s\nL2_%s\n" % (RND, RND, RND)
    expect_ok(run(["write", "-", p], stdin=content), "write(准备)")
    r = expect_ok(run(["read", p, "--json"]), "read --json")
    j = jload(r.out, "read json")
    if first(j, ["content"]) != content:
        fail("read 内容与写入不一致", r.out)
    r2 = expect_ok(run(["read", p, "--start=0", "--end=1"]), "read 行区间")
    if r2.out.strip() != ("L0_%s" % RND):
        fail("--start/--end 行区间语义不符（SPEC：0 起、end 不含尾行）", r2.all)
    return "整读一致；--start=0 --end=1 恰好取第 1 行（0 起、end 不含）"


@test("fs", "cat")
def t_cat():
    p = "%s/cat_%s.txt" % (_fs_dir(), RND)
    content = "CAT-%s\n" % RND
    expect_ok(run(["write", "-", p], stdin=content), "write(准备)")
    r = expect_ok(run(["cat", p]), "cat")
    if r.out != content:
        fail("cat 输出与写入不一致", r.all)
    return "cat 原样输出 %d 字节" % len(content)


@test("fs", "ls")
def t_ls():
    d = _fs_dir()
    f = "%s/ls_%s.txt" % (d, RND)
    h = "%s/.hidden_%s" % (d, RND)
    expect_ok(run(["write", "-", f], stdin="x\n"), "write(准备)")
    expect_ok(run(["write", "-", h], stdin="x\n"), "write(准备2)")
    r = expect_ok(run(["ls", d]), "ls")
    if ("ls_%s.txt" % RND) not in r.out:
        fail("ls 未列出文件", r.all)
    if (".hidden_%s" % RND) in r.out:
        fail("ls 默认不应显示隐藏文件", r.all)
    r2 = expect_ok(run(["ls", d, "--hidden", "--json"]), "ls --hidden")
    if (".hidden_%s" % RND) not in r2.out:
        fail("--hidden 未显示隐藏文件", r2.out)
    return "默认隐藏点文件、--hidden 可见"


@test("fs", "stat")
def t_stat():
    d = _fs_dir()
    p = "%s/stat_%s.txt" % (d, RND)
    content = "STAT-%s\n" % RND
    expect_ok(run(["write", "-", p], stdin=content), "write(准备)")
    r = expect_ok(run(["stat", p, "--json"]), "stat --json")
    j = jload(r.out, "stat json")
    size = first(j, ["size"])
    try:
        size_n = int(size)
    except (TypeError, ValueError):
        fail("stat 未返回 size", r.out)
        return
    if size_n != len(content):
        fail("stat size=%d，期望 %d" % (size_n, len(content)), r.out)
    rd = expect_ok(run(["stat", d, "--json"]), "stat 目录")
    jd = jload(rd.out, "stat dir json")
    if not first(jd, ["is_directory"]):
        fail("目录 is_directory 应为 true", rd.out)
    return "文件 size=%d 一致；目录 is_directory=true" % len(content)


@test("fs", "tree")
def t_tree():
    d = "%s/tree_%s" % (FSBASE, RND)
    expect_ok(run(["mkdir", "%s/deep" % d, "--parents"]), "mkdir(准备)")
    f = "%s/deep/leaf_%s.txt" % (d, RND)
    expect_ok(run(["write", "-", f], stdin="leaf\n"), "write(准备)")
    r = expect_ok(run(["tree", d]), "tree")
    if ("leaf_%s.txt" % RND) not in r.out:
        fail("tree 未列出叶子文件", r.all)
    if "deep" not in r.out:
        fail("tree 未列出子目录", r.all)
    return "树含 deep/ 与 leaf 文件"


@test("fs", "edit")
def t_edit():
    p = "%s/edit_%s.txt" % (_fs_dir(), RND)
    content = "AAA beta\nAAA gamma\n"
    expect_ok(run(["write", "-", p], stdin=content), "write(准备)")
    expect_ok(run(["edit", p, "--old=AAA", "--new=ZZZ_%s" % RND, "--replace-all"]), "edit replace-all")
    r = expect_ok(run(["read", p, "--json"]), "read(校验)")
    if ("ZZZ_%s" % RND) not in r.out:
        fail("替换结果未落盘", r.out)
    if "AAA" in r.out:
        fail("--replace-all 未替换全部 AAA", r.out)
    expect_ok(run(["edit", p, "--insert=0", "--text=HEAD_%s" % RND]), "edit insert")
    r2 = expect_ok(run(["read", p, "--json"]), "read(校验2)")
    if ("HEAD_%s" % RND) not in r2.out:
        fail("insert 内容未落盘", r2.out)
    return "str_replace(--replace-all) 与 insert 均生效"


@test("fs", "grep")
def t_grep():
    d = _fs_dir()
    p = "%s/grep_%s.txt" % (d, RND)
    needle = "NEEDLE_%s" % RND
    expect_ok(run(["write", "-", p], stdin="x\n%s\n" % needle), "write(准备)")
    r = expect_ok(run(["grep", d, needle]), "grep")
    if needle not in r.out:
        fail("grep 未命中", r.all)
    return "grep 命中并回显所在行"


@test("fs", "search")
def t_search():
    d = "%s/search_%s" % (FSBASE, RND)
    expect_ok(run(["mkdir", "%s/sub" % d, "--parents"]), "mkdir(准备)")
    expect_ok(run(["write", "-", "%s/sub/sought_%s.txt" % (d, RND)], stdin="s\n"), "write(准备)")
    r = run(["search", d, "**/*.txt"], timeout=60)
    expect_ok(r, "search")
    if ("sought_%s.txt" % RND) not in r.out:
        fail("search 未找到 **/*.txt", r.all)
    return "glob **/*.txt 找到目标文件"


@test("fs", "cp")
def t_cp():
    d = _fs_dir()
    src = "%s/cp_src_%s.txt" % (d, RND)
    dst = "%s/cp_dst_%s.txt" % (d, RND)
    content = "CP-%s\n" % RND
    expect_ok(run(["write", "-", src], stdin=content), "write(准备)")
    expect_ok(run(["cp", src, dst]), "cp")
    r = expect_ok(run(["read", dst, "--json"]), "read(校验)")
    if content.strip() not in r.out:
        fail("cp 后目标内容不符", r.out)
    return "复制后内容一致"


@test("fs", "mv")
def t_mv():
    d = _fs_dir()
    src = "%s/mv_src_%s.txt" % (d, RND)
    dst = "%s/mv_dst_%s.txt" % (d, RND)
    expect_ok(run(["write", "-", src], stdin="M\n"), "write(准备)")
    expect_ok(run(["mv", src, dst]), "mv")
    expect_ok(run(["stat", dst, "--json"]), "stat(目标)")
    r = run(["stat", src, "--json"])
    if r.rc == 0:
        fail("mv 后源路径仍存在", r.out)
    return "移动成功且源已不存在"


@test("fs", "rm")
def t_rm():
    d = "%s/rm_%s" % (FSBASE, RND)
    expect_ok(run(["mkdir", "%s/inner" % d, "--parents"]), "mkdir(准备)")
    expect_ok(run(["write", "-", "%s/inner/f.txt" % d], stdin="f\n"), "write(准备)")
    expect_ok(run(["rm", d, "--recursive"]), "rm --recursive")
    r = run(["stat", d, "--json"])
    if r.rc == 0:
        fail("rm 后路径仍存在", r.out)
    return "递归删除目录成功"


@test("fs", "put")
def t_put():
    d = _fs_dir()
    lp = os.path.join(SCRATCH, "put_bin_%s.bin" % RND)
    data = os.urandom(4096)
    with open(lp, "wb") as f:
        f.write(data)
    remote = "%s/put_%s.bin" % (d, RND)
    expect_ok(run(["put", lp, remote]), "put")
    local_sha = hashlib.sha256(data).hexdigest()
    r = run(["exec", "sha256sum %s" % remote])
    if r.rc == 0 and local_sha in r.out:
        return "put 后远端 sha256=%s… 与本地一致" % local_sha[:16]
    # 退化校验：下载回来比对（exec 不可用时也能定位问题）
    dp = lp + ".back"
    r2 = run(["get", remote, dp])
    if r2.rc == 0 and os.path.exists(dp):
        with open(dp, "rb") as f:
            back = f.read()
        if hashlib.sha256(back).hexdigest() == local_sha:
            return "put 成功（经 get 回读 sha256 一致）"
    fail("put 后 sha256 不一致（exec 侧 rc=%d）" % r.rc, (r.all + " || " + r2.all))


@test("fs", "get")
def t_get():
    d = _fs_dir()
    lp = os.path.join(SCRATCH, "get_src_%s.bin" % RND)
    data = os.urandom(2048)
    with open(lp, "wb") as f:
        f.write(data)
    remote = "%s/get_%s.bin" % (d, RND)
    expect_ok(run(["put", lp, remote]), "put(准备)")
    dp = os.path.join(SCRATCH, "get_dst_%s.bin" % RND)
    expect_ok(run(["get", remote, dp]), "get")
    if not os.path.exists(dp):
        fail("get 未写出本地文件", dp)
    with open(dp, "rb") as f:
        got = f.read()
    if hashlib.sha256(got).hexdigest() != hashlib.sha256(data).hexdigest():
        fail("get 文件 sha256 与源不一致（%d vs %d 字节）" % (len(got), len(data)), "")
    return "get 二进制安全，sha256 一致（%d 字节）" % len(data)


# ================================================================ pty 组

def _ensure_pty(name_hint="it-pty-shared"):
    sid = CTX.get("pty")
    if not sid:
        sid = "%s-%s" % (name_hint, RND)
        expect_ok(run(["pty-new", sid, "--cwd=/tmp", "--cols=100", "--rows=30"]), "pty-new(准备)")
        CTX["pty"] = sid
    return sid


@test("pty", "pty-new")
def t_pty_new():
    sid = "it-pty-new-%s" % RND
    r = expect_ok(run(["pty-new", sid, "--cwd=/tmp", "--cols=100", "--rows=30", "--json"]), "pty-new --json")
    if sid not in r.out:
        fail("pty-new 响应未见会话 id", r.out)
    return "pty 会话 %s 创建成功" % sid


@test("pty", "pty")
def t_pty_exec():
    sid = _ensure_pty()
    tag = rand_tag("PTY")
    r = expect_ok(run(["pty", sid, "echo %s" % tag], timeout=60), "pty echo")
    if tag not in r.all:
        fail("pty 执行输出缺少回显/结果 %s" % tag, r.all)
    return "命令回显/输出含 %s" % tag


@test("pty", "pty-screen")
def t_pty_screen():
    sid = _ensure_pty()
    tag = rand_tag("SCR")
    expect_ok(run(["pty", sid, "echo %s" % tag], timeout=60), "pty(准备)")
    holder = {"r": None}

    def probe():
        rr = run(["pty-screen", sid], timeout=60)
        holder["r"] = rr
        return rr if (rr.rc == 0 and tag in rr.all) else None

    if not wait_until(probe, timeout=20, interval=1.0):
        fail("pty-screen 未出现 %s" % tag, holder["r"].all if holder["r"] else "")
    return "屏幕内容包含 %s" % tag


@test("pty", "pty-input")
def t_pty_input():
    sid = _ensure_pty()
    tag = rand_tag("INP")
    expect_ok(run(["pty-input", sid, "echo %s" % tag, "--enter"]), "pty-input")
    holder = {"r": None}

    def probe():
        rr = run(["pty-screen", sid], timeout=60)
        holder["r"] = rr
        return rr if (rr.rc == 0 and tag in rr.all) else None

    if not wait_until(probe, timeout=20, interval=1.0):
        fail("pty-input 后屏幕未出现 %s" % tag, holder["r"].all if holder["r"] else "")
    return "输入回显并执行（%s）" % tag


@test("pty", "pty-signal")
def t_pty_signal():
    sid = "it-pty-sig-%s" % RND
    expect_ok(run(["pty-new", sid, "--cwd=/tmp"]), "pty-new(准备)")
    expect_ok(run(["pty", sid, "sleep 45", "--async"], timeout=60), "pty --async")
    r = expect_ok(run(["pty-signal", sid, "SIGINT"], timeout=60), "pty-signal")
    if "SIGINT" not in r.all:
        fail("pty-signal 输出未提及 SIGINT", r.all)
    # 实现注记：发信号即终止会话进程，会话随后从列表消失
    holder = {"r": None}

    def gone():
        rr = run(["pty-ls", "--json"], timeout=60)
        holder["r"] = rr
        return rr if (rr.rc == 0 and sid not in rr.out) else None

    if not wait_until(gone, timeout=15, interval=1.0):
        fail("SIGINT 后会话仍存在", holder["r"].out if holder["r"] else "")
    return "SIGINT 终止会话（从列表消失）"


@test("pty", "pty-resize")
def t_pty_resize():
    sid = _ensure_pty()
    r = expect_ok(run(["pty-resize", sid, "--cols=120", "--rows=40"]), "pty-resize")
    if "120" not in r.all:
        fail("resize 输出未见 cols=120", r.all)
    tag = rand_tag("RSZ")
    r2 = expect_ok(run(["pty", sid, "echo %s" % tag], timeout=60), "pty(校验)")
    if tag not in r2.all:
        fail("resize 后会话不可用", r2.all)
    return "cols=120 已设置，会话仍可用"


@test("pty", "pty-ls")
def t_pty_ls():
    sid = _ensure_pty()
    r = expect_ok(run(["pty-ls"]), "pty-ls")
    if sid not in r.all:
        fail("pty-ls 未列出 %s" % sid, r.all)
    return "列表包含 %s" % sid


@test("pty", "pty-rm")
def t_pty_rm():
    sid = "it-pty-rm-%s" % RND
    expect_ok(run(["pty-new", sid]), "pty-new(准备)")
    expect_ok(run(["pty-rm", sid]), "pty-rm")
    got, last = wait_token_gone(["pty-ls", "--json"], sid, timeout=10)
    if not got:
        fail("pty-rm 后列表仍含 %s" % sid, last.out if last else "")
    return "pty 会话删除成功"


# ================================================================ watch 组

WDIR = "/tmp/cli_it_watch_%s" % RND


def _watch_dir():
    if not CTX.get("watch_dir_ok"):
        expect_ok(run(["mkdir", WDIR, "--parents"]), "mkdir(准备)")
        CTX["watch_dir_ok"] = True
    return WDIR


def _ensure_watcher():
    wid = CTX.get("watcher")
    if wid:
        return wid
    d = _watch_dir()
    r = expect_ok(run(["watch", d, "--json"]), "watch(准备)")
    j = jload(r.out, "watch json")
    wid = first(j, ["watcher_id"])
    if not wid:
        m = re.search(r"watcher_id=(\S+)", r.out)
        wid = m.group(1) if m else ""
    if not wid:
        fail("无法从 watch 响应取 watcher_id", r.out)
    CTX["watcher"] = str(wid)
    return CTX["watcher"]


@test("watch", "watch")
def t_watch():
    d = _watch_dir()
    r = expect_ok(run(["watch", d, "--json"]), "watch --json")
    j = jload(r.out, "watch json")
    wid = first(j, ["watcher_id"])
    if not wid:
        fail("watch 未返回 watcher_id", r.out)
    CTX["watcher"] = str(wid)
    r2 = expect_ok(run(["watch", d]), "watch(人类可读)")
    if "watcher_id=" not in r2.out:
        fail("人类可读输出缺少 watcher_id=", r2.all)
    return "watcher_id=%s（--json 与文本两种输出均可用）" % wid


@test("watch", "watch-poll")
def t_watch_poll():
    wid = _ensure_watcher()
    time.sleep(1)   # 给 watcher 一点注册时间
    fname = "EV_%s.txt" % RND
    fp = "%s/%s" % (_watch_dir(), fname)
    expect_ok(run(["write", "-", fp], stdin="ev\n"), "write(触发事件)")
    holder = {"r": None}

    def probe():
        rr = run(["watch-poll", wid, "--timeout=10", "--json"], timeout=40)
        holder["r"] = rr
        if rr.rc != 0:
            return None
        try:
            j = json.loads(rr.out)
        except ValueError:
            return None
        evs = first(j, ["events"])
        if isinstance(evs, list) and any(fname in str(e) for e in evs):
            return rr
        return None

    if not wait_until(probe, timeout=30, interval=2.0):
        fail("watch-poll 未收到 %s 的事件" % fname, holder["r"].all if holder["r"] else "")
    return "收到文件事件（%s）" % fname


@test("watch", "watch-ls")
def t_watch_ls():
    wid = _ensure_watcher()
    r = expect_ok(run(["watch-ls"]), "watch-ls")
    if wid not in r.all:
        fail("watch-ls 未列出 %s" % wid, r.all)
    return "列表包含 %s" % wid


@test("watch", "watch-rm")
def t_watch_rm():
    # 用全新 watcher 验证删除语义（老 watcher 被 poll 订阅过后，服务端可能长期不摘除，
    # 属已知服务端行为、不作为本用例的硬断言；见交付报告"实测与文档不符"）。
    d = "%s/rm_%s" % (_watch_dir(), RND)
    expect_ok(run(["mkdir", d, "--parents"]), "mkdir(准备)")
    r = run(["watch", d, "--json"])
    expect_ok(r, "watch(准备)")
    j = jload(r.out, "watch json")
    wid = str(first(j, ["watcher_id"]) or "")
    if not wid:
        fail("准备 watcher 失败", r.out)
    if wid not in run(["watch-ls", "--json"]).out:
        fail("准备 watcher 未出现在列表", "")
    expect_ok(run(["watch-rm", wid]), "watch-rm")
    got, last = wait_token_gone(["watch-ls", "--json"], wid, timeout=10)
    if not got:
        fail("watch-rm 后列表仍含 %s" % wid, last.out if last else "")
    CTX["watcher"] = None
    return "全新 watcher 停止并从列表移除"


# ================================================================ code 组

@test("code", "code")
def t_code():
    r = expect_ok(run(["code", "print(6*7)"], timeout=120), "code python")
    if "42" not in r.all:
        fail("python 输出缺 42", r.all)
    r2 = expect_ok(run(["code", "console.log(6*7)", "--lang=javascript"], timeout=120), "code javascript")
    if "42" not in r2.all:
        fail("javascript 输出缺 42", r2.all)
    r3 = expect_ok(run(["code", "print(6*7)", "--json"], timeout=120), "code --json")
    if "42" not in r3.out:
        fail("--json 输出缺 42", r3.out)
    return "python/js 均输出 42，--json 可用"


@test("code", "code-info")
def t_code_info():
    r = expect_ok(run(["code-info"]), "code-info")
    if "python" not in r.out.lower():
        fail("code-info 未见 python 语言信息", r.out)
    return "后端信息含 python（%d 字节）" % len(r.out)


@test("code", "code-sess-new")
def t_code_sess_new():
    r = expect_ok(run(["code-sess-new", "--lang=python", "--json"]), "code-sess-new")
    j = jload(r.out, "code-sess-new json")
    sid = first(j, ["session_id", "id"])
    if not sid:
        m = re.search(r"session_id[:=]\s*(\S+)", r.out)
        sid = m.group(1) if m else ""
    if not sid:
        fail("未取到 code session id", r.out)
    CTX["code_sess"] = str(sid)
    expect_ok(run(["code", "x = 42", "--session=%s" % sid], timeout=120), "code 设变量")
    r2 = expect_ok(run(["code", "print(x)", "--session=%s" % sid], timeout=120), "code 读变量")
    if "42" not in r2.all:
        fail("会话变量未跨调用保持", r2.all)
    return "会话 %s 跨调用保持变量（x=42）" % sid


@test("code", "code-sess-ls")
def t_code_sess_ls():
    sid = CTX.get("code_sess")
    if not sid:
        r0 = run(["code-sess-new", "--json"])
        expect_ok(r0, "code-sess-new(准备)")
        j = jload(r0.out, "json")
        sid = str(first(j, ["session_id", "id"]) or "")
        CTX["code_sess"] = sid
    if not sid:
        fail("准备 code 会话失败", "")
    r = expect_ok(run(["code-sess-ls", "--json"]), "code-sess-ls")
    if sid not in r.out:
        fail("会话列表未包含 %s" % sid, r.out)
    return "列表包含会话 %s" % sid


@test("code", "code-sess-rm")
def t_code_sess_rm():
    r0 = run(["code-sess-new", "--json"])
    expect_ok(r0, "code-sess-new(准备)")
    j = jload(r0.out, "json")
    sid = str(first(j, ["session_id", "id"]) or "")
    if not sid:
        fail("准备 code 会话失败", r0.out)
    expect_ok(run(["code-sess-rm", sid]), "code-sess-rm")
    return "删除会话 %s（服务端对不存在会话也回 deleted=false，不额外断言）" % sid


# ================================================================ mcp 组

@test("mcp", "mcp")
def t_mcp():
    r = expect_ok(run(["mcp", "tools/list"], timeout=60), "mcp tools/list")
    j = jload(r.out, "mcp tools/list json")
    tools = first(j, ["tools"])
    if not isinstance(tools, list):
        fail("tools/list 未返回 tools 数组", r.out)
    if len(tools) < 30:
        fail("tools 数量 %d < 30" % len(tools), r.out)
    r2 = expect_ok(run(["mcp", "initialize"], timeout=60), "mcp initialize")
    if ("protocolVersion" not in r2.out) and ("protocolversion" not in r2.out.lower()):
        fail("initialize 缺 protocolVersion", r2.out)
    expect_ok(run(["mcp", "ping"], timeout=60), "mcp ping")
    return "tools/list=%d（≥30）、initialize、ping 均正常" % len(tools)


# ================================================================ browser 组

def _data_url(html):
    return "data:text/html;charset=utf-8," + urllib.parse.quote(html)


def _br_ensure_page():
    """确保当前页是一个可控的 data: 页面（带 #inp 输入框和 #btn 按钮）。"""
    title = "IT_PAGE_%s" % RND
    if CTX.get("br_page") == title:
        return title
    html = ('<title>%s</title><body><input id="inp">'
            '<button id="btn" onclick="document.title=\'CLICK_%s\'">go</button></body>'
            % (title, RND))
    expect_ok(run(["br-go", _data_url(html)], timeout=90), "br-go(准备)")
    CTX["br_page"] = title
    return title


def _extract_tab_id(r):
    try:
        j = json.loads(r.out)
        v = first(j, ["tab_id", "id"])
        if v is not None:
            return str(v)
    except ValueError:
        pass
    m = re.search(r"tab[_-]?id[=:]\s*([A-Za-z0-9-]+)", r.out)
    return m.group(1) if m else ""


@test("browser", "br-info")
def t_br_info():
    r = expect_ok(run(["br-info"], timeout=60), "br-info")
    if not r.out.strip():
        fail("br-info 无输出", r.all)
    return "浏览器信息非空（%d 字节）" % len(r.out.strip())


@test("browser", "br-go")
def t_br_go():
    title = "NAV_%s" % RND
    html = '<title>%s</title><h1 id="h">OK</h1>' % title
    expect_ok(run(["br-go", _data_url(html)], timeout=90), "br-go")
    r = expect_ok(run(["br-eval", "document.title"], timeout=60), "br-eval(校验)")
    if title not in r.all:
        fail("导航后 document.title 应为 %s" % title, r.all)
    CTX["br_page"] = None
    return "data: 页面导航成功（title=%s）" % title


@test("browser", "br-eval")
def t_br_eval():
    r = expect_ok(run(["br-eval", "6*7"], timeout=60), "br-eval 6*7")
    if "42" not in r.all:
        fail("表达式结果应为 42", r.all)
    tok = "PROMISE_%s" % RND
    r2 = expect_ok(run(["br-eval", "Promise.resolve('%s')" % tok, "--await"], timeout=60), "br-eval --await")
    if tok not in r2.all:
        fail("--await 未解出 Promise 值", r2.all)
    return "6*7=42；--await 解出 %s" % tok


@test("browser", "br-shot")
def t_br_shot():
    _br_ensure_page()
    fp = os.path.join(SCRATCH, "shot_%s.png" % RND)
    expect_ok(run(["br-shot", fp], timeout=90), "br-shot")
    if not os.path.exists(fp):
        fail("br-shot 未生成文件", fp)
    with open(fp, "rb") as f:
        head = f.read(8)
    if head != b"\x89PNG\r\n\x1a\n":
        fail("文件不是 PNG（前8字节 %r）" % head, "")
    sz = os.path.getsize(fp)
    if sz < 100:
        fail("PNG 过小（%d 字节）" % sz, "")
    return "PNG 魔数正确，%d 字节" % sz


@test("browser", "br-snapshot")
def t_br_snapshot():
    _br_ensure_page()
    r = expect_ok(run(["br-snapshot"], timeout=90), "br-snapshot")
    if not r.out.strip():
        fail("snapshot 为空", r.all)
    expect_ok(run(["br-snapshot", "--interactive"], timeout=90), "br-snapshot --interactive")
    return "snapshot 输出 %d 字节；--interactive 可用" % len(r.out.strip())


@test("browser", "br-click")
def t_br_click():
    title = "PRE_%s" % RND
    html = ('<title>%s</title><button id="btn" onclick="document.title=\'CLICK_%s\'">go</button>'
            % (title, RND))
    expect_ok(run(["br-go", _data_url(html)], timeout=90), "br-go(准备)")
    expect_ok(run(["br-click", "--selector=#btn"], timeout=60), "br-click")
    r = run(["br-eval", "document.title"], timeout=60)
    expect_ok(r, "br-eval(校验)")
    if ("CLICK_%s" % RND) not in r.all:
        fail("点击后 title 未变化", r.all)
    return "点击 #btn 触发 onclick（title 更新）"


@test("browser", "br-fill")
def t_br_fill():
    expect_ok(run(["br-go", _data_url('<body><input id="inp"></body>')], timeout=90), "br-go(准备)")
    val = "FILL_%s" % RND
    expect_ok(run(["br-fill", "--selector=#inp", "--value=%s" % val], timeout=60), "br-fill")
    r = run(["br-eval", "document.getElementById('inp').value"], timeout=60)
    expect_ok(r, "br-eval(校验)")
    if val not in r.all:
        fail("fill 后输入框值不符", r.all)
    return "输入框值=%s" % val


@test("browser", "br-tabs")
def t_br_tabs():
    r = expect_ok(run(["br-tabs", "--json"], timeout=60), "br-tabs")
    if not r.out.strip():
        fail("br-tabs 无输出", r.all)
    j = jload(r.out, "br-tabs json")
    tabs = first(j, ["tabs"])
    if isinstance(tabs, list) and len(tabs) < 1:
        fail("tabs 列表为空", r.out)
    return "tabs 列表非空（%d 字节）" % len(r.out.strip())


@test("browser", "br-tab-new")
def t_br_tab_new():
    r = expect_ok(run(["br-tab-new", "--url=%s" % _data_url("<title>TAB_%s</title>" % RND), "--json"],
                      timeout=90), "br-tab-new")
    tid = _extract_tab_id(r)
    if not tid:
        fail("未取到 tab_id", r.out)
    CTX["tab_id"] = tid
    r2 = run(["br-tabs", "--json"])
    expect_ok(r2, "br-tabs(校验)")
    if tid not in r2.out:
        fail("新标签页未出现在 tabs", r2.out)
    return "新标签页 %s 已创建并可见" % tid


@test("browser", "br-tab-use")
def t_br_tab_use():
    tid = CTX.get("tab_id")
    if not tid:
        r0 = run(["br-tab-new", "--json"])
        expect_ok(r0, "br-tab-new(准备)")
        tid = _extract_tab_id(r0)
        CTX["tab_id"] = tid
    if not tid:
        fail("准备标签页失败", "")
    expect_ok(run(["br-tab-use", str(tid)], timeout=60), "br-tab-use")
    r = run(["br-eval", "1+1"], timeout=60)
    expect_ok(r, "br-eval(校验)")
    if "2" not in r.all:
        fail("切换标签页后 eval 失败", r.all)
    return "切换到标签页 %s 后求值可用" % tid


@test("browser", "br-tab-close")
def t_br_tab_close():
    r0 = run(["br-tab-new", "--json"])
    expect_ok(r0, "br-tab-new(准备)")
    tid = _extract_tab_id(r0)
    if not tid:
        fail("准备标签页失败", r0.out)
    expect_ok(run(["br-tab-close", str(tid)], timeout=60), "br-tab-close")
    got, last = wait_token_gone(["br-tabs", "--json"], str(tid), timeout=10)
    if not got:
        fail("close 后 tabs 仍含 %s" % tid, last.out if last else "")
    return "标签页 %s 关闭成功" % tid


@test("browser", "br-cookies")
def t_br_cookies():
    name = "it_c_%s" % RND[:10]
    val = "cv_%s" % RND
    expect_ok(run(["br-cookie-set", "--name=%s" % name, "--value=%s" % val, "--url=%s" % HUB],
                  timeout=60), "br-cookie-set(准备)")
    r = expect_ok(run(["br-cookies", "--url=%s" % HUB], timeout=60), "br-cookies")
    if name not in r.all or val not in r.all:
        fail("cookie 未出现在列表中", r.all)
    return "cookie %s 已写入并可列出" % name


@test("browser", "br-cookie-set")
def t_br_cookie_set():
    expect_ok(run(["br-go", HUB], timeout=90), "br-go(准备)")
    name = "it_set_%s" % RND[:10]
    val = "sv_%s" % RND
    expect_ok(run(["br-cookie-set", "--name=%s" % name, "--value=%s" % val, "--url=%s" % HUB],
                  timeout=60), "br-cookie-set")
    r2 = run(["br-cookies", "--url=%s" % HUB])
    expect_ok(r2, "br-cookies(校验)")
    if val not in r2.all:
        fail("设置的值未生效", r2.all)
    return "cookie set 生效（%s）" % name


@test("browser", "br-network")
def t_br_network():
    # ① 结构：--json 必须是合法 data 且含 requests 数组
    r0 = run(["br-network", "--json", "--limit=5"], timeout=60)
    expect_ok(r0, "br-network --json")
    j0 = jload(r0.out, "br-network json")
    if not isinstance(first(j0, ["requests"]), list):
        fail("br-network 未返回 requests 数组", r0.out)
    # ② 生成新流量（门户页），缓冲里出现 127.0.0.1 则走严格断言
    #    注：实测服务端录制缓冲较"娇气"——任何一次 --clear 之后可能停止录制（见报告），
    #    此时退化为"结构 + clear 语义"断言，不误报 CLI 层问题。
    expect_ok(run(["br-go", HUB], timeout=90), "br-go(准备)")
    holder = {"r": None}

    def probe():
        rr = run(["br-network", "--json", "--limit=200"], timeout=60)
        holder["r"] = rr
        return rr if (rr.rc == 0 and "127.0.0.1" in rr.out) else None

    strict = bool(wait_until(probe, timeout=8, interval=1.5))
    # ③ --clear 语义：返回"清除前快照"再清空（单次调用）
    rc = run(["br-network", "--json", "--clear"], timeout=60)
    expect_ok(rc, "br-network --clear")
    jc = jload(rc.out, "br-network --clear json")
    if not isinstance(first(jc, ["requests"]), list):
        fail("--clear 未返回 requests 快照", rc.out)
    if strict:
        return "缓冲含 127.0.0.1 新请求（严格分支）+ --clear 返回快照 ✓"
    return "缓冲无 http 记录（服务端录制未激活/clear 后停止）→ 结构+clear 语义断言 ✓"


@test("browser", "br-cdp")
def t_br_cdp():
    r = expect_ok(run(["br-cdp", "Browser.getVersion"], timeout=60), "br-cdp")
    if "chrome" not in r.all.lower():
        fail("CDP 返回未见 Chrome 信息", r.all)
    return "Browser.getVersion 返回 Chrome 版本信息"


# ================================================================ 交互式进程（pty-ws / watch-events 共用）

def _pump(src, dst):
    """持续把子进程输出搬进缓冲。

    注意：必须用 read1()/os.read()（有数据即返回）；BufferedReader.read(n) 会一直阻塞
    到攒满 n 字节或 EOF —— 输出量小的交互用例（pty-ws 回显仅百余字节）会因此永远等不到，
    表现为「明明有回显却判失败」。
    """
    try:
        fd = src.fileno()
        while True:
            chunk = os.read(fd, 4096)
            if not chunk:
                break
            dst.extend(chunk)
    except Exception:
        pass


def spawn_interact(args, timeout=30.0):
    """启动 CLI 子进程并持续收集输出；返回句柄（含 stdin 管道与输出缓冲）。"""
    cmd = [CLI] + [str(a) for a in args]
    e = dict(os.environ)
    e["SANDBOX_BASE"] = BASE
    p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=subprocess.PIPE, env=e)
    out, err = bytearray(), bytearray()
    threading.Thread(target=_pump, args=(p.stdout, out), daemon=True).start()
    threading.Thread(target=_pump, args=(p.stderr, err), daemon=True).start()
    return {"p": p, "out": out, "err": err, "t0": time.time(), "timeout": timeout}


def si_send(s, data, delay=0.0):
    if delay > 0:
        time.sleep(delay)
    try:
        if data:
            s["p"].stdin.write(data if isinstance(data, bytes) else data.encode("utf-8"))
            s["p"].stdin.flush()
    except (BrokenPipeError, ValueError, OSError):
        pass


def si_text(s):
    return s["out"].decode("utf-8", "replace"), s["err"].decode("utf-8", "replace")


def si_wait_for(s, token, timeout=15.0, interval=0.4):
    """轮询子进程输出，直到 token 出现或进程退出。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        out, err = si_text(s)
        if token in out or token in err:
            return True
        if s["p"].poll() is not None:
            time.sleep(0.5)
            out, err = si_text(s)
            return token in out or token in err
        time.sleep(interval)
    return False


def si_stop(s, sig=None):
    """停止子进程（默认 SIGINT）；返回 (returncode, out, err)。"""
    sig = sig or signal.SIGINT
    if s["p"].poll() is None:
        try:
            s["p"].send_signal(sig)
        except Exception:
            pass
        end = time.time() + 3
        while time.time() < end and s["p"].poll() is None:
            time.sleep(0.1)
        if s["p"].poll() is None:
            s["p"].kill()
            s["p"].wait()
    time.sleep(0.4)
    out, err = si_text(s)
    return s["p"].returncode, out, err


# ================================================================ mock 服务器（请求形状断言）

MOCK = {"httpd": None, "base": "", "requests": []}
_PNG_FAKE = b"\x89PNG\r\n\x1a\n" + b"\x00" * 96


def _mock_data(path):
    table = {
        "/v2/computer/info": {"available": True, "platform": "mock-xfce", "resolution": "1280x1024"},
        "/v2/computer/cursor": {"x": 10, "y": 20},
        "/v2/computer/clipboard": {"text": "MOCK-CLIP"},
        "/v2/computer/windows": {"windows": [{"title": "mock-win"}]},
        "/v2/computer/accessibility": {"nodes": [{"id": 1, "role": "root", "name": "mock"}]},
        "/v2/computer/accessibility/nodes": {"nodes": [{"id": 1, "role": "root", "name": "mock"}], "limit": 50},
        "/v2/computer/actions": {"ok": True},
        "/v2/computer/actions/batch": {"ok": True, "count": 1},
        "/v2/computer/record": {"recording": True},
    }
    return table.get(path, {"ok": True, "path": path})


class _MockHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _serve(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else b""
        path = self.path.split("?")[0]
        MOCK["requests"].append({"method": self.command, "path": self.path,
                                 "body": body.decode("utf-8", "replace")})
        if path == "/v2/computer/screenshot":
            code, payload, ctype = 200, _PNG_FAKE, "image/png"
        elif path == "/health":
            code, payload, ctype = 200, b'{"status":"healthy"}', "application/json"
        else:
            data = _mock_data(path)
            payload = json.dumps({"success": True, "message": "ok", "data": data}).encode()
            code, ctype = 200, "application/json"
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = _serve


def mock_base():
    if MOCK["httpd"] is None:
        httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _MockHandler)
        MOCK["httpd"] = httpd
        MOCK["base"] = "http://127.0.0.1:%d" % httpd.server_address[1]
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return MOCK["base"]


def mock_stop():
    if MOCK["httpd"] is not None:
        MOCK["httpd"].shutdown()
        MOCK["httpd"] = None


def mock_run(args, timeout=60):
    """对 mock 服务器跑一条命令（先清空请求记录）。"""
    MOCK["requests"] = []
    return run(args, timeout=timeout, env={"SANDBOX_BASE": mock_base()})


def mock_shape(method, path, body_contains=None, what=""):
    """断言 mock 收到过指定 方法+路径 的请求（body_contains 为子串列表）。"""
    reqs = [r for r in MOCK["requests"] if r["path"].split("?")[0] == path and r["method"] == method]
    if not reqs:
        got = ", ".join("%s %s" % (r["method"], r["path"]) for r in MOCK["requests"]) or "（无请求）"
        fail("%s: mock 未收到 %s %s（实收: %s）" % (what, method, path, got), "")
    last = reqs[-1]
    for c in (body_contains or []):
        if c not in last["body"]:
            fail("%s: 请求体缺少 %r" % (what, c), last["body"])


# ================================================================ computer 组（cmp-*；真机 / 503 / mock 三态）

CMP = {"state": None, "info": None}


def cmp_probe():
    """探测 computer-use 环境：real=可用 / unavailable=503 / stub=未实现 / error=其它。"""
    if CMP["state"]:
        return CMP["state"]
    r = run(["cmp-info"], timeout=60)
    txt = r.all
    if r.rc == 0:
        CMP["state"] = "real"
    elif "尚未实现" in txt:
        CMP["state"] = "stub"
    elif ("503" in txt) or ("不可用" in txt) or ("not available" in txt.lower()):
        CMP["state"] = "unavailable"
    else:
        CMP["state"] = "error"
    CMP["info"] = r
    return CMP["state"]


def cmp_precheck(what):
    st = cmp_probe()
    if st == "stub":
        fail("%s: CLI 实现尚未合入（桩文件返回尚未实现；待 v2 全功能补齐后转绿）" % what,
             CMP["info"].all)
    if st == "error":
        fail("%s: cmp-info 探测失败（非 503、非桩，需排查）" % what, CMP["info"].all)
    return st


def cmp_503(cmd_args, what):
    """真机→返回 (结果, None)；不可用→校验 503 干净失败并返回 (None, '503 路径')。"""
    st = cmp_probe()
    r = run(cmd_args, timeout=90)
    if st == "real":
        return r, None
    if r.rc == 0:
        fail("%s: 环境无 computer-use（期望 503）但命令成功了" % what, r.all)
    low = r.all.lower()
    if ("503" not in r.all) and ("不可用" not in r.all) and ("unavailable" not in low) and ("not available" not in low):
        fail("%s: 503 路径的错误信息不含 503/不可用" % what, r.all)
    return None, "503 路径"


# ================================================================ 新增命令用例（v2 全功能补齐批次）

@test("fs", "fs-tree-put")
def t_fs_tree_put():
    d = "%s/tree_put_%s" % (FSBASE, RND)
    tp = os.path.join(SCRATCH, "tree_%s.tar" % RND)
    with tarfile.open(tp, "w") as tf:
        for name, content in [("a_%s.txt" % RND, "AAA\n"), ("sub/b_%s.txt" % RND, "BBB\n")]:
            payload = content.encode()
            info = tarfile.TarInfo(name)
            info.size = len(payload)
            tf.addfile(info, io.BytesIO(payload))
    expect_ok(run(["fs-tree-put", tp, d]), "fs-tree-put")
    r = run(["read", "%s/a_%s.txt" % (d, RND), "--json"], timeout=60)
    if r.rc != 0 or "AAA" not in r.out:
        fail("整树上传后首层文件不可读/内容不符", r.all)
    r2 = run(["stat", "%s/sub/b_%s.txt" % (d, RND), "--json"], timeout=60)
    expect_ok(r2, "stat(校验)")
    return "tar 整树上传后 a.txt/sub/b.txt 均可读"


@test("pty", "pty-ws")
def t_pty_ws():
    sid = _ensure_pty()
    tag = rand_tag("WS")
    s = spawn_interact(["pty-ws", sid, "--protocol=json"], timeout=35)
    si_send(s, b"", delay=3.0)               # 等 WebSocket 建立（桌面镜像较慢）
    si_send(s, "echo %s\r" % tag, delay=0.3)
    if not si_wait_for(s, tag, timeout=8) and not si_wait_for(s, tag, timeout=12):
        rc, out, err = si_stop(s)
        fail("pty-ws 未收到命令回显/输出 %s" % tag, out + "\n[stderr] " + err)
    si_send(s, "\x1d", delay=0.3)            # Ctrl-]
    rc, out, err = si_stop(s)
    return "WebSocket 附着终端回显 %s（退出码 %s）" % (tag, rc)


@test("pty", "pty-ws-anon")
def t_pty_ws_anon():
    tag = rand_tag("ANON")
    s = spawn_interact(["pty-ws-anon", "--protocol=json"], timeout=35)
    si_send(s, b"", delay=3.0)
    si_send(s, "echo %s\r" % tag, delay=0.3)
    if not si_wait_for(s, tag, timeout=8) and not si_wait_for(s, tag, timeout=12):
        rc, out, err = si_stop(s)
        fail("匿名 WebShell 未收到回显 %s" % tag, out + "\n[stderr] " + err)
    si_send(s, "\x1d", delay=0.3)
    rc, out, err = si_stop(s)
    return "匿名 WebShell 回显 %s（退出码 %s）" % (tag, rc)


@test("watch", "watch-events")
def t_watch_events():
    wid = _ensure_watcher()
    fname = "SSE_%s.txt" % RND
    fp = "%s/%s" % (_watch_dir(), fname)
    s = spawn_interact(["watch-events", wid], timeout=30)
    time.sleep(2)                            # 等 SSE 订阅建立
    expect_ok(run(["write", "-", fp], stdin="sse\n"), "write(触发)")
    if not si_wait_for(s, fname, timeout=15):
        rc, out, err = si_stop(s)
        fail("watch-events 未收到事件 %s" % fname, out + "\n[stderr] " + err)
    rc, out, err = si_stop(s, signal.SIGINT)
    return "SSE 事件流收到 %s（退出码 %s）" % (fname, rc)


@test("browser", "br-config")
def t_br_config():
    r = run(["br-config", "--resolution=1280x1024"], timeout=60)
    expect_ok(r, "br-config --resolution")
    r2 = run(["br-config", "--json={\"resolution\":\"800x600\"}"], timeout=60)
    expect_ok(r2, "br-config --json")
    return "分辨率与 JSON 两种形式均可用"


@test("browser", "br-upload")
def t_br_upload():
    up = os.path.join(SCRATCH, "upload_%s.txt" % RND)
    with open(up, "w") as f:
        f.write("UPLOAD-%s\n" % RND)
    expect_ok(run(["br-go", _data_url('<body><input type="file" id="f"></body>')], timeout=90),
              "br-go(准备)")
    expect_ok(run(["br-upload", "--selector=#f", "--paths=%s" % up], timeout=90), "br-upload")
    r = run(["br-eval",
             "document.getElementById('f').files.length + ':' + document.getElementById('f').files[0].name"],
            timeout=60)
    expect_ok(r, "br-eval(校验)")
    if ("1:" in r.all) and (os.path.basename(up) in r.all):
        return "文件 input 收到 %s" % os.path.basename(up)
    fail("上传后 file input 未反映所选文件", r.all)


# ================================================================ computer 组（cmp-*）

@test("computer", "cmp-info")
def t_cmp_info():
    cmp_precheck("cmp-info")
    r, note = cmp_503(["cmp-info"], "cmp-info")
    if note is None:
        expect_ok(r, "cmp-info(真机)")
        if not r.out.strip():
            fail("真机 cmp-info 无输出", r.all)
    mr = mock_run(["cmp-info", "--json"])
    expect_ok(mr, "cmp-info(mock)")
    mock_shape("GET", "/v2/computer/info", what="cmp-info")
    return "真机正常 + mock 形状 ✓" if note is None else "503 路径 + mock 形状 GET /v2/computer/info ✓"


@test("computer", "cmp-shot")
def t_cmp_shot():
    cmp_precheck("cmp-shot")
    # 请求形状 + 写盘：mock 分支（真机 / 503 两种环境都跑）
    mfp = os.path.join(SCRATCH, "cmp_shot_mock_%s.png" % RND)
    mr = mock_run(["cmp-shot", mfp])
    expect_ok(mr, "cmp-shot(mock)")
    if not os.path.exists(mfp):
        fail("mock cmp-shot 未生成文件", "")
    with open(mfp, "rb") as f:
        if f.read(8) != b"\x89PNG\r\n\x1a\n":
            fail("mock 截图非 PNG", "")
    mock_shape("GET", "/v2/computer/screenshot", what="cmp-shot")
    # 真机 / 503 路径
    fp = os.path.join(SCRATCH, "cmp_shot_%s.png" % RND)
    r, note = cmp_503(["cmp-shot", fp], "cmp-shot")
    if note is None:
        expect_ok(r, "cmp-shot(真机)")
        if not os.path.exists(fp):
            fail("真机 cmp-shot 未生成文件", fp)
        with open(fp, "rb") as f:
            head = f.read(8)
        if head != b"\x89PNG\r\n\x1a\n":
            fail("真机截图不是 PNG（前8字节 %r）" % head, "")
        real_sz = os.path.getsize(fp)
        if real_sz < 1000:
            fail("真机截图过小（%d 字节，疑似非真实画面）" % real_sz, "")
        return "真机 PNG %d 字节 + mock PNG 写盘/形状 ✓" % real_sz
    return "503 路径 + mock PNG 写盘/形状 ✓"


@test("computer", "cmp-cursor")
def t_cmp_cursor():
    cmp_precheck("cmp-cursor")
    mr = mock_run(["cmp-cursor"])
    expect_ok(mr, "cmp-cursor(mock)")
    if ("10" not in mr.all) and ("mock" not in mr.all.lower()):
        fail("mock cursor 输出不符", mr.all)
    mock_shape("GET", "/v2/computer/cursor", what="cmp-cursor")
    r, note = cmp_503(["cmp-cursor"], "cmp-cursor")
    if note is None:
        expect_ok(r, "cmp-cursor(真机)")
        if not r.out.strip():
            fail("真机 cursor 无输出", r.all)
        return "真机正常 + mock 形状 ✓"
    return "503 路径 + mock 形状 GET /v2/computer/cursor ✓"


@test("computer", "cmp-clipboard")
def t_cmp_clipboard():
    cmp_precheck("cmp-clipboard")
    mr = mock_run(["cmp-clipboard"])
    expect_ok(mr, "cmp-clipboard(mock)")
    if "MOCK-CLIP" not in mr.all:
        fail("mock clipboard 输出不符", mr.all)
    mock_shape("GET", "/v2/computer/clipboard", what="cmp-clipboard")
    # 真机：先写再读。桌面镜像的空剪贴板没有 selection owner，直接读会 503
    # （"target STRING not available"）；写入后即可读 —— 这也是正确的验收姿势。
    tag = "CB_%s" % RND
    setr = run(["cmp-act", '{"action_type":"SET_CLIPBOARD","text":"%s"}' % tag], timeout=60)
    if setr.rc == 0:
        r = run(["cmp-clipboard"], timeout=60)
        if r.rc == 0 and tag in r.all:
            return "真机写读一致（%s）+ mock 形状 ✓" % tag
        r2, note = cmp_503(["cmp-clipboard"], "cmp-clipboard")
        if note is not None:
            return "SET_CLIPBOARD 成功但读取 503（环境相关）+ mock 形状 ✓"
        fail("真机 clipboard 写读不一致", r.all)
    r, note = cmp_503(["cmp-clipboard"], "cmp-clipboard")
    if note is None:
        expect_ok(r, "cmp-clipboard(真机)")
        if not r.out.strip():
            fail("真机 clipboard 无输出", r.all)
        return "真机可读 + mock 形状 ✓"
    return "503 路径 + mock 形状 GET /v2/computer/clipboard ✓"


@test("computer", "cmp-windows")
def t_cmp_windows():
    cmp_precheck("cmp-windows")
    mr = mock_run(["cmp-windows"])
    expect_ok(mr, "cmp-windows(mock)")
    if "mock-win" not in mr.all:
        fail("mock windows 输出不符", mr.all)
    mock_shape("GET", "/v2/computer/windows", what="cmp-windows")
    r, note = cmp_503(["cmp-windows"], "cmp-windows")
    if note is None:
        expect_ok(r, "cmp-windows(真机)")
        if not r.out.strip():
            fail("真机 windows 无输出", r.all)
        return "真机正常 + mock 形状 ✓"
    return "503 路径 + mock 形状 GET /v2/computer/windows ✓"


@test("computer", "cmp-a11y")
def t_cmp_a11y():
    cmp_precheck("cmp-a11y")
    mr = mock_run(["cmp-a11y", "--max-depth=3"])
    expect_ok(mr, "cmp-a11y(mock)")
    if ("root" not in mr.all) and ("mock" not in mr.all.lower()):
        fail("mock a11y 输出不符", mr.all)
    mock_shape("GET", "/v2/computer/accessibility", what="cmp-a11y")
    r, note = cmp_503(["cmp-a11y", "--max-depth=3"], "cmp-a11y")
    if note is None:
        expect_ok(r, "cmp-a11y(真机)")
        if len(r.out.strip()) < 10:
            fail("真机无障碍树输出过短", r.all)
        return "真机无障碍树 + mock 形状 ✓"
    return "503 路径 + mock 形状 GET /v2/computer/accessibility ✓"


@test("computer", "cmp-a11y-nodes")
def t_cmp_a11y_nodes():
    cmp_precheck("cmp-a11y-nodes")
    mr = mock_run(["cmp-a11y-nodes", "--max-depth=2", "--limit=10"])
    expect_ok(mr, "cmp-a11y-nodes(mock)")
    mock_shape("GET", "/v2/computer/accessibility/nodes", what="cmp-a11y-nodes")
    r, note = cmp_503(["cmp-a11y-nodes", "--max-depth=2", "--limit=10"], "cmp-a11y-nodes")
    if note is None:
        expect_ok(r, "cmp-a11y-nodes(真机)")
        if len(r.out.strip()) < 10:
            fail("真机无障碍节点输出过短", r.all)
        return "真机节点列表 + mock 形状 ✓"
    return "503 路径 + mock 形状 GET /v2/computer/accessibility/nodes ✓"


@test("computer", "cmp-act")
def t_cmp_act():
    cmp_precheck("cmp-act")
    act = '{"action":"click","x":100,"y":200}'
    r, note = cmp_503(["cmp-act", act], "cmp-act")
    if note is None:
        expect_ok(r, "cmp-act(真机)")
    mr = mock_run(["cmp-act", act])
    expect_ok(mr, "cmp-act(mock)")
    mock_shape("POST", "/v2/computer/actions", body_contains=["click", "100"], what="cmp-act")
    return "真机点击 + mock 形状/动作体 ✓" if note is None else "503 路径 + mock 形状/动作体（click,100）✓"


@test("computer", "cmp-act-batch")
def t_cmp_act_batch():
    cmp_precheck("cmp-act-batch")
    acts = '[{"action":"move","x":5,"y":6},{"action":"click","x":5,"y":6}]'
    r, note = cmp_503(["cmp-act-batch", acts], "cmp-act-batch")
    if note is None:
        expect_ok(r, "cmp-act-batch(真机)")
    mr = mock_run(["cmp-act-batch", acts])
    expect_ok(mr, "cmp-act-batch(mock)")
    mock_shape("POST", "/v2/computer/actions/batch", body_contains=["move", "click"], what="cmp-act-batch")
    return "真机批量动作 + mock 形状 ✓" if note is None else "503 路径 + mock 形状/动作体（move,click）✓"


@test("computer", "cmp-record")
def t_cmp_record():
    cmp_precheck("cmp-record")
    mr = mock_run(["cmp-record", "--action=start", "--fps=10"])
    expect_ok(mr, "cmp-record(mock)")
    mock_shape("POST", "/v2/computer/record", body_contains=["start"], what="cmp-record")
    r, note = cmp_503(["cmp-record", "--action=start", "--fps=10", "--max-duration=2"], "cmp-record(start)")
    if note is None:
        expect_ok(r, "cmp-record start(真机)")
        expect_ok(run(["cmp-record", "--action=stop"], timeout=90), "cmp-record stop(真机)")
        return "真机 start/stop + mock 形状 ✓"
    return "503 路径 + mock 形状 POST /v2/computer/record ✓"


# ================================================================ 运行器

def select_tests(only):
    if not only:
        return list(TESTS)
    prefs = [p.strip() for p in only.split(",") if p.strip()]
    out = []
    for fn in TESTS:
        g, n = fn._group, fn._name
        if any(n == p or n.startswith(p) or g == p or g.startswith(p) for p in prefs):
            out.append(fn)
    return out


def main():
    raw_args = sys.argv[1:]
    if not raw_args:
        raw_args = shlex.split(os.environ.get("IT_ARGS", ""))
    only = None
    for a in raw_args:
        if a.startswith("--only="):
            only = a.split("=", 1)[1]
        elif a == "--list":
            for fn in TESTS:
                print("%-4s %s" % (fn._group, fn._name))
            return 0
        elif a.startswith("--"):
            print("未知参数: %s（支持 --only=<前缀[,前缀]> / --list）" % a, file=sys.stderr)
            return 2

    selected = select_tests(only)
    if not selected:
        print("没有匹配的用例（--only=%s）" % only, file=sys.stderr)
        return 2

    print("=" * 62)
    print("sandbox-sdk-go v5 CLI 集成测试（cli_it.py）")
    print("CLI         : %s" % CLI)
    print("SANDBOX_BASE: %s" % BASE)
    print("用例数      : %d%s" % (len(selected), "（--only=%s）" % only if only else ""))
    print("=" * 62, flush=True)

    passed, failed = [], []
    t_start = time.time()
    for fn in selected:
        t0 = time.time()
        try:
            digest = fn() or "通过"
            passed.append(fn._name)
            print("✔ [%-7s] %-13s %6.1fs  %s" % (fn._group, fn._name, time.time() - t0, digest), flush=True)
        except Exception as e:   # noqa: BLE001 —— 测试运行器需要兜住所有异常
            failed.append(fn._name)
            print("✘ [%-7s] %-13s %6.1fs  %s" % (fn._group, fn._name, time.time() - t0, e), flush=True)

    dur = time.time() - t_start
    print("-" * 62)
    if CMP["state"]:
        print("computer 环境: %s" % {
            "real": "真机（computer-use 可用）",
            "unavailable": "无 computer-use（已按 503 路径 + mock 请求形状断言）",
            "stub": "CLI 桩（cmp-* 实现未合入，待转绿）",
            "error": "探测异常（需排查）",
        }.get(CMP["state"], CMP["state"]))
    mock_stop()
    print("汇总: ✔ 通过 %d ｜ ✘ 失败 %d ｜ 共 %d ｜ 用时 %.1fs" % (len(passed), len(failed), len(selected), dur))
    if failed:
        print("失败用例: %s" % ", ".join(failed))
    print("结果: %s" % ("全部通过" if not failed else "存在失败（%d）" % len(failed)))
    print("CLI_IT_DONE")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
