#!/bin/sh
# run.sh —— cli_it.py 一键集成测试（建沙箱 → 编译 CLI → 跑测试 → 打印报告 → 销毁沙箱）
#
# 流程：
#   1) 开沙箱。模板选择（SANDBOX_TEMPLATE=auto 时自动判定）：
#      ① 模板列表里有 alias=aio-computer 且 READY → 用它（真机验收：cmp-* 真 PNG/点击/无障碍等）
#      ② 否则回退 tpl-cbd89e4aaf7743bba9a74ccc（aio-computer；cmp-* 真机可用）
#      申请失败自动回退轻量模板 tpl-fb13c778ed3741e8aec51d3e
#   2) 打包当前工作树（go.mod + *.go）上传，在沙箱内 go build 出 amd64 CLI（/tmp/cli）
#   3) 上传 tests/cli_it.py，在沙箱内后台执行（SANDBOX_BASE=http://127.0.0.1:8080，
#      即沙箱自身的 v2 API），轮询取回完整报告
#   4) 打印报告；以 cli_it.py 的退出码退出（0=全过）——注意：桩/未合入命令必然非 0，属预期
#   5) 无论成败都销毁沙箱（trap；调试可用 KEEP_SANDBOX=1 保留）
#
# 用法:
#   sh tests/run.sh                       # 跑全部用例（当前工作树）
#   sh tests/run.sh --only=fs             # 只跑某一组/前缀（可逗号分隔）
#   STUB=1 sh tests/run.sh --only=exec,fs # 自检：编译 .scaffold 纯桩版本，验证 ✘ 标记链路
#
# 环境变量:
#   SANDBOX_TEMPLATE          模板；默认 auto（aio-computer → tpl-c262），可显式指定
#   SANDBOX_FALLBACK_TEMPLATE 回退模板（默认 tpl-fb13c778ed3741e8aec51d3e）
#   CUBE_API_KEY / CUBESANDBOX_API_KEY  平台密钥（二者其一）
#   KEEP_SANDBOX=1            调试时保留沙箱（默认销毁）
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TESTS_DIR="$ROOT/tests"
CSB=${CUBESANDBOX_CMD:-/usr/local/bin/cubesandbox-sdk-go}
TEMPLATE=${SANDBOX_TEMPLATE:-auto}
FALLBACK=${SANDBOX_FALLBACK_TEMPLATE:-tpl-fb13c778ed3741e8aec51d3e}
API_URL=${CUBESANDBOX_API_URL:?请先 export CUBESANDBOX_API_URL=https://<cubesandbox-api-host>}
IT_ARGS="$*"
SID=""
TMP=$(mktemp -d /tmp/cli_it_run.XXXXXX)

export CUBE_API_KEY="${CUBE_API_KEY:-${CUBESANDBOX_API_KEY:-}}"

say() { printf '%s\n' "$*"; }
die() { say "!! 错误: $*" >&2; exit 1; }

# 找 alias=aio-computer 且 READY 的模板（没有则输出空；兼容 aliases 列表 / alias 字符串两种形状）
aio_computer_template() {
  curl -s -H "X-API-KEY: $CUBE_API_KEY" "$API_URL/templates" | python3 -c '
import sys, json
try:
    t = json.load(sys.stdin)
except Exception:
    sys.exit(0)
items = t if isinstance(t, list) else t.get("templates", [])
for x in items:
    al = x.get("aliases")
    if al is None:
        al = x.get("alias")
    if isinstance(al, str):
        al = [al]
    if "aio-computer" in (al or []) and str(x.get("status") or "").upper() == "READY":
        print(x.get("templateID")); break
'
}

cleanup() {
  if [ -n "$SID" ]; then
    if [ "${KEEP_SANDBOX:-0}" = "1" ]; then
      say "[清理] KEEP_SANDBOX=1，保留沙箱: $SID"
    else
      say "[清理] 销毁沙箱 $SID ..."
      "$CSB" rm "$SID" >/dev/null 2>&1 && say "[清理] 沙箱已销毁: $SID" \
        || say "[清理] !! 销毁失败，请手动 rm: $SID"
    fi
  fi
}
trap cleanup EXIT INT TERM

# 上传（带重试；失败时把 stderr 带进报错，便于定位）
csb_put() {
  n=0
  while [ $n -lt 3 ]; do
    if "$CSB" put "$SID" "$1" "$2" >/dev/null 2>"$TMP/put.err"; then
      return 0
    fi
    n=$((n+1))
    [ $n -lt 3 ] && sleep 3
  done
  die "上传 $1 → $2 失败: $(tail -2 "$TMP/put.err" | tr '\n' ' ')"
}

[ -x "$CSB" ] || command -v "$CSB" >/dev/null 2>&1 || die "找不到 $CSB"
[ -n "$CUBE_API_KEY" ] || die "未设置 CUBE_API_KEY / CUBESANDBOX_API_KEY"
[ -f "$TESTS_DIR/cli_it.py" ] || die "缺少 $TESTS_DIR/cli_it.py"

# 模板自动判定（aio-computer 优先）
CMP_ENV_NOTE=""
if [ "$TEMPLATE" = "auto" ]; then
  say "[模板] 查询模板列表（找 alias=aio-computer）..."
  CMP_TPL=$(aio_computer_template)
  if [ -n "$CMP_TPL" ]; then
    TEMPLATE="$CMP_TPL"
    CMP_ENV_NOTE="aio-computer 已就绪（$CMP_TPL）→ cmp-* 真机验收"
  else
    TEMPLATE="tpl-cbd89e4aaf7743bba9a74ccc"
    CMP_ENV_NOTE="未等到真机环境（aio-computer 未注册/未 READY）→ cmp-* 走 503 路径 + mock 断言"
  fi
else
  CMP_ENV_NOTE="SANDBOX_TEMPLATE 显式指定（$TEMPLATE）"
fi

say "==== sandbox-sdk-go v5 CLI 集成测试 (run.sh) ===="
say "工作树 : $ROOT"
say "模板   : $TEMPLATE（回退 $FALLBACK）"
say "computer: $CMP_ENV_NOTE"
say "测试参数: ${IT_ARGS:-（全部用例）}"

# ---------------------------------------------------------------- 1) 建沙箱
say ""
say "[1/5] 申请沙箱 ..."
try_n=0
while [ -z "$SID" ] && [ $try_n -lt 3 ]; do
  for tmpl in "$TEMPLATE" "$FALLBACK"; do
    out=$("$CSB" new --timeout=3600 --note=cli-it --template="$tmpl" 2>&1 || true)
    cand=$(printf '%s' "$out" | python3 -c 'import sys,re;m=re.findall(r"[0-9a-f]{32}",sys.stdin.read());print(m[-1] if m else "")')
    if [ -n "$cand" ]; then
      SID="$cand"; USED_TPL="$tmpl"; break
    fi
    say "      ⚠ 模板 $tmpl 申请未成功：$(printf '%s' "$out" | tail -1 | cut -c1-140)"
  done
  if [ -z "$SID" ]; then
    try_n=$((try_n+1))
    [ $try_n -lt 3 ] && { say "      … $try_n/3 轮失败，等待 45s 重试"; sleep 45; }
  fi
done
[ -n "$SID" ] || die "3 轮均未能创建沙箱（容量受限？稍后再试）"
say "      沙箱: $SID（模板 $USED_TPL，timeout=3600s）"

# ---------------------------------------------------------------- 2) 就绪等待
say "[2/5] 等待沙箱内 v2 API 就绪（/health=200）..."
ready=0; i=0
while [ $i -lt 60 ]; do
  r=$("$CSB" exec "$SID" 'curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/health' 2>/dev/null | tr -d '\r"')
  case "$r" in
    *200*) ready=1; break ;;
  esac
  sleep 5; i=$((i+1))
done
[ $ready = 1 ] || die "沙箱 API 90s 内未就绪"
say "      就绪（等待 ${i} 轮）"

# ---------------------------------------------------------------- 3) 上传 + 编译
if [ "${STUB:-0}" = "1" ]; then SRC="$ROOT/.scaffold"; MODE="stub"; else SRC="$ROOT"; MODE="worktree"; fi
say "[3/5] 打包并编译 CLI（模式=$MODE）..."
( cd "$SRC" && tar czf "$TMP/ws.tgz" go.mod *.go ) || die "打包工作树失败"
NFILES=$(tar tzf "$TMP/ws.tgz" | wc -l | tr -d ' ')
csb_put "$TMP/ws.tgz" /tmp/ws.tgz
csb_put "$TESTS_DIR/cli_it.py" /tmp/cli_it.py

cat > "$TMP/build.sh" <<'EOS'
set -e
rm -rf /tmp/w && mkdir -p /tmp/w
tar xzf /tmp/ws.tgz -C /tmp/w
cd /tmp/w
export PATH="$PATH:/usr/local/go/bin"
echo "== 文件清单 =="
ls *.go | tr '\n' ' '; echo
echo "== gofmt -l =="
gofmt -l . || true
echo "== go build =="
if go build -o /tmp/cli . ; then echo BUILD-OK; else echo BUILD-FAIL; fi
EOS
csb_put "$TMP/build.sh" /tmp/build.sh
bout=""; n=0
while [ $n -lt 3 ]; do
  bout=$("$CSB" exec "$SID" 'sh /tmp/build.sh' 2>&1 || true)
  case "$bout" in
    *BUILD-OK*|*BUILD-FAIL*) break ;;
  esac
  n=$((n+1)); sleep 3
done
say "      包内文件数=$NFILES"
printf '%s\n' "$bout" | sed 's/^/      /'
case "$bout" in
  *BUILD-OK*) say "      编译成功（BUILD-OK）" ;;
  *) die "编译失败（详见上方输出）" ;;
esac

# ---- aio-computer 镜像：Chromium 是 manual 模式，跑浏览器用例前用桌面启动器拉起（幂等）----
case "${USED_TPL:-$TEMPLATE}" in
  tpl-cbd89e4aaf7743bba9a74ccc)
    bstat=$("$CSB" exec "$SID" 'curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9222/json/version' 2>/dev/null | tail -1)
    if [ "$bstat" != "200" ]; then
      say "      [准备] aio-computer 浏览器为 manual 模式 → 调 /opt/gem/browser-launch.sh 拉起"
      "$CSB" exec "$SID" '/opt/gem/browser-launch.sh >/dev/null 2>&1 || true; sleep 8; curl -s -o /dev/null -w "cdp=%{http_code}\n" http://127.0.0.1:9222/json/version' 2>/dev/null | tail -1 | sed 's/^/      /'
    fi
    ;;
esac

# ---------------------------------------------------------------- 4) 跑测试
say "[4/5] 运行 cli_it.py（沙箱内后台执行，本地轮询）..."
cat > "$TMP/run_tests.sh" <<EOS
cd /tmp
rm -f /tmp/cli_it.log
nohup sh -c 'CLI=/tmp/cli SANDBOX_BASE=http://127.0.0.1:8080 IT_ARGS="$IT_ARGS" python3 /tmp/cli_it.py >> /tmp/cli_it.log 2>&1; echo CLI_IT_EXIT=\$? >> /tmp/cli_it.log' >/dev/null 2>&1 &
echo RUNNER-LAUNCHED
EOS
csb_put "$TMP/run_tests.sh" /tmp/run_tests.sh
launch=""; n=0
while [ $n -lt 3 ]; do
  launch=$("$CSB" exec "$SID" 'sh /tmp/run_tests.sh' 2>&1 || true)
  case "$launch" in
    *RUNNER-LAUNCHED*) break ;;
  esac
  n=$((n+1)); sleep 3
done
case "$launch" in
  *RUNNER-LAUNCHED*) ;;
  *) die "启动测试进程失败: $(printf '%s' "$launch" | tail -1)" ;;
esac
i=0; done=0
while [ $i -lt 150 ]; do
  tl=$("$CSB" exec "$SID" 'tail -c 4000 /tmp/cli_it.log 2>/dev/null || true' 2>/dev/null || true)
  case "$tl" in
    *CLI_IT_EXIT=*) done=1; break ;;
  esac
  i=$((i+1))
  if [ $((i % 6)) -eq 0 ]; then
    say "      … 运行中（已等 $((i*8))s）"
  fi
  sleep 8
done
[ $done = 1 ] || die "等待 cli_it.py 结束超时（20 分钟）"

if ! "$CSB" get "$SID" /tmp/cli_it.log "$TMP/report.txt" >/dev/null 2>&1; then
  "$CSB" cat "$SID" /tmp/cli_it.log > "$TMP/report.txt" 2>/dev/null || die "取回测试日志失败"
fi

say ""
say "==================== 测试报告（cli_it.py） ===================="
cat "$TMP/report.txt"
say "=============================================================="

res=$(grep -o 'CLI_IT_EXIT=[0-9]*' "$TMP/report.txt" | tail -1 | cut -d= -f2)
[ -n "$res" ] || res=3

# ---------------------------------------------------------------- 5) 汇总
say ""
say "==== run.sh 摘要 ===="
say "  沙箱        : $SID（模板 $USED_TPL）"
say "  computer    : $CMP_ENV_NOTE"
say "  构建模式    : $MODE"
say "  cli_it 退出码: $res（0=全部通过；非 0=有失败，桩/未合入命令属预期）"
if [ -n "${REPORT_OUT:-}" ]; then
  cp "$TMP/report.txt" "$REPORT_OUT" && say "  报告已另存: $REPORT_OUT"
fi
say "  本地留存    : $TMP/report.txt（随 /tmp 清理策略）"

# 沙箱销毁交给 trap（EXIT）
exit "$res"
