# -*- coding: utf-8 -*-
# cmp_sim.py —— 用模拟 CLI 行为验证 computer 组的 真机/503/mock 三分支逻辑（不含真实实现）
import json
import sys
import urllib.request

sys.path.insert(0, "/tmp")
import cli_it as t  # noqa: E402

scenario = sys.argv[1] if len(sys.argv) > 1 else "unavailable"
t.CMP["state"] = scenario  # 直接指定环境状态，跳过真实探测

_ROUTES = {
    "cmp-info": ("GET", "/v2/computer/info", None),
    "cmp-cursor": ("GET", "/v2/computer/cursor", None),
    "cmp-clipboard": ("GET", "/v2/computer/clipboard", None),
    "cmp-windows": ("GET", "/v2/computer/windows", None),
    "cmp-a11y": ("GET", "/v2/computer/accessibility", None),
    "cmp-a11y-nodes": ("GET", "/v2/computer/accessibility/nodes", None),
}
_PNG_BIG = b"\x89PNG\r\n\x1a\n" + b"\x00" * 1500


def sim_run(args, stdin=None, timeout=None, env=None):
    base = (env or {}).get("SANDBOX_BASE", t.BASE)
    cmd = args[0]
    if base == t.BASE:
        # 真机尝试：可用→成功；不可用→503
        if scenario == "real":
            if cmd == "cmp-shot":
                with open(args[1], "wb") as f:
                    f.write(_PNG_BIG)
                return t.RunResult(0, "已保存截图: %s\n" % args[1], "", args)
            return t.RunResult(0, '{"ok":true,"platform":"sim-xfce"}\n', "", args)
        return t.RunResult(1, "", "错误: 请求失败: HTTP 503: computer-use 服务不可用（hint: 需 aio-computer 镜像）", args)
    # mock 分支：向 mock 发真实请求（模拟未来实现的请求形状）
    if cmd == "cmp-shot":
        data = urllib.request.urlopen(base + "/v2/computer/screenshot").read()
        with open(args[1], "wb") as f:
            f.write(data)
        return t.RunResult(0, "已保存截图\n", "", args)
    if cmd == "cmp-act-batch":
        body = args[1] if len(args) > 1 else "[]"
        urllib.request.urlopen(urllib.request.Request(base + "/v2/computer/actions/batch",
                                                      data=body.encode(), method="POST"))
        return t.RunResult(0, '{"ok":true}\n', "", args)
    if cmd == "cmp-act":
        body = args[1] if len(args) > 1 else "{}"
        urllib.request.urlopen(urllib.request.Request(base + "/v2/computer/actions",
                                                      data=body.encode(), method="POST"))
        return t.RunResult(0, '{"ok":true}\n', "", args)
    if cmd == "cmp-record":
        urllib.request.urlopen(urllib.request.Request(base + "/v2/computer/record",
                                                      data=b'{"action":"start","fps":10}', method="POST"))
        return t.RunResult(0, '{"recording":true}\n', "", args)
    if cmd in _ROUTES:
        meth, pth, _ = _ROUTES[cmd]
        data = urllib.request.urlopen(base + pth).read()
        return t.RunResult(0, data.decode("utf-8", "replace"), "", args)
    return t.run(args, stdin=stdin, timeout=timeout, env=env)


t.run = sim_run  # 让 mock_run / cmp_* 都走模拟

fails = 0
for fn in list(t.TESTS):
    if fn._group != "computer":
        continue
    try:
        digest = fn()
        print("PASS %-14s %s" % (fn._name, digest))
    except Exception as e:
        fails += 1
        print("FAIL %-14s %s" % (fn._name, e))
print("== 场景 %s：%d 失败 ==" % (scenario, fails))
t.mock_stop()
sys.exit(1 if fails else 0)
