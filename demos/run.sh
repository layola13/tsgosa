#!/bin/bash
# tsgosa/demos/run.sh — TS 特性进度 runner: main.ts -> tsgo --sa -> main.sai -> sa build-exe -> diff expected.stdout
# 用法: ./run.sh [-j N] [--check] [demo目录名...]   (无参数=全量)
#   -j N / --jobs N: 并行跑 N 例（默认 CPU 数；输出表仍按目录序打印，PASS/FAIL 与串行一致）
#   --corpus: 对 sa_plugin_ts/demos 语料跑上游/薄口通拒差分（零分歧即过；同样受 -j 控制）
# --check: 只重生成 .sai 并与进仓版逐字节比对(.sai 禁止手改,只能由编译器出;供 CI/提交前自证)
set -u
set -o pipefail
export PATH=$PATH:/opt/zig:/usr/local/go/bin

JOBS=0
CHECK=0
CORPUS=0
while [ "$#" -gt 0 ]; do
  case "${1:-}" in
    --check) CHECK=1; shift ;;
    --corpus) CORPUS=1; shift ;;
    -j|--jobs) JOBS="${2:-0}"; if [ "$#" -ge 2 ]; then shift 2; else shift; fi ;;
    -j*|--jobs=*) JOBS="${1#-j}"; JOBS="${JOBS#--jobs=}"; shift ;;
    --) shift; break ;;
    -*) echo "unknown flag: $1" >&2; exit 2 ;;
    *) break ;;
  esac
done
case "$JOBS" in
  ''|*[!0-9]*|0) JOBS=$(nproc 2>/dev/null || echo 4) ;;
esac

HERE=$(dirname "$(realpath "$0")")
TSGOSA=$(dirname "$HERE")
TSGO_BIN=${TSGO_BIN:-/tmp/tsgo-demo}
SA_BIN=${SA_BIN:-/content/sa_all/sci/zig-out/bin/sa}

if [ ! -x "$TSGO_BIN" ]; then
  echo "building tsgo ..."
  (cd "$TSGOSA" && go build -o "$TSGO_BIN" ./cmd/tsgo) || exit 2
elif [ -n "$(find "$TSGOSA/cmd/tsgo" "$TSGOSA/internal/transpile" -name '*.go' -newer "$TSGO_BIN" 2>/dev/null | head -1)" ]; then
  echo "rebuilding stale tsgo ..."
  (cd "$TSGOSA" && go build -o "$TSGO_BIN" ./cmd/tsgo) || exit 2
fi
if [ ! -x "$SA_BIN" ]; then
  echo "error: sa backend not found at $SA_BIN (build sci: zig build -Dllvm=false)" >&2
  exit 2
fi

# ---- corpus 差分模式: 上游 tsgo-sa vs 薄口 tsgo --sa,逐例比通/拒 ----
if [ "$CORPUS" -eq 1 ]; then
  SATSGO_DIR=${SATSGO_DIR:-/content/sa_all/satsgo}
  CORPUS_DIR=${CORPUS_DIR:-/content/sa_all/sa_plugin_ts/demos}
  UP_BIN=${UP_BIN:-/tmp/tsgo-sa-upstream}
  if [ -n "$(find "$SATSGO_DIR/cmd/tsgo-sa" "$SATSGO_DIR/internal/saemit" -name '*.go' -newer "$UP_BIN" 2>/dev/null | head -1)" ] || [ ! -x "$UP_BIN" ]; then
    echo "building upstream tsgo-sa ..."
    (cd "$SATSGO_DIR" && go build -o "$UP_BIN" ./cmd/tsgo-sa) || exit 2
  fi
  [ -d "$CORPUS_DIR" ] || { echo "error: corpus not found at $CORPUS_DIR" >&2; exit 2; }
  run_one_corpus() {
    d="$1"
    f="$CORPUS_DIR/$d"
    src=""
    [ -f "$f/main.ts" ] && src="$f/main.ts"
    [ -z "$src" ] && return 0
    o1=$(mktemp -d); o2=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$o1' '$o2'" RETURN
    "$UP_BIN" --out "$o1" "$src" >/dev/null 2>&1
    if grep -q "refused=true" "$o1/subset-report.txt" 2>/dev/null; then up_ok=0; else up_ok=1; fi
    if "$TSGO_BIN" --sa --out "$o2" "$src" >/dev/null 2>&1; then
      base=$(basename "$src" .ts)
      # 警告行（warning:）不翻 verdict；只有非警告行才算拒绝。
      if [ -f "$o2/$base.sai" ] && ! grep -qv "warning:" "$o2/subset-report.txt" 2>/dev/null; then port_ok=1; else port_ok=0; fi
    else
      port_ok=0
    fi
    if [ "$up_ok" -eq "$port_ok" ]; then
      echo "$d AGREE up=$up_ok"
    else
      echo "$d MISMATCH up=$up_ok port=$port_ok"
    fi
  }
  export TSGO_BIN UP_BIN CORPUS_DIR
  export -f run_one_corpus 2>/dev/null || true
  tmp_res=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp_res'" EXIT
  names=$(ls "$CORPUS_DIR")
  # shellcheck disable=SC2086
  echo "$names" | xargs -P "$JOBS" -I{} bash -c 'run_one_corpus "$@" >"'"$tmp_res"'/$1.out" 2>&1' _ {} || true
  agree=0; mismatch=0; bad=""
  for d in $names; do
    line=$(cat "$tmp_res/$d.out" 2>/dev/null || echo "$d MISSING")
    case "$line" in
      *" AGREE "*) agree=$((agree+1)) ;;
      *" MISMATCH "*) mismatch=$((mismatch+1)); bad="$bad $d(${line##* })" ;;
      *) mismatch=$((mismatch+1)); bad="$bad $d(unknown)" ;;
    esac
  done
  echo "== agree=$agree mismatch=$mismatch =="
  [ -n "$bad" ] && echo "mismatched:$bad"
  [ "$mismatch" -eq 0 ]
  exit $?
fi

if [ "$#" -gt 0 ]; then
  DEMOS="$*"
else
  DEMOS=$(ls "$HERE")
fi
# 非 demo 条目（README.md/run.sh 等）沿旧语义静默跳过
FILTERED=""
for n in $DEMOS; do
  [ -f "$HERE/$n/main.ts" ] || continue
  FILTERED="$FILTERED $n"
done
DEMOS="$FILTERED"

run_one_demo() {
  n="$1"
  d="$HERE/$n"
  [ -f "$d/main.ts" ] || return 0
  if [ ! -f "$d/expected.stdout" ] && [ ! -f "$d/expect.refused" ]; then echo "$n SKIP no expected.stdout"; return 0; fi
  out="$d/out"
  rm -rf "$out"
  mkdir -p "$out"
  # .sai 落 demo 根（随批进仓供检查；其余生成物进 out/（忽略）
  # --check 模式: 生成到临时位,只与进仓 main.sai 比对,不碰工作区
  if [ "$CHECK" -eq 1 ]; then
    out="$out/check.$n"
    mkdir -p "$out"
    SA_OUT="$out/main.sai"
  else
    SA_OUT="$d"
  fi
  if [ -f "$d/package.json" ]; then
    # 真实 program 工程（package.json + 多文件 + npm deps）：tsgo build 出 sci
    # workspace，合并 main.sai 即编译产物（--check 比对进仓 $d/main.sai）。
    # 预期拒收工程（$d/expect.refused，每行一指纹）：build 必须拒收且指纹全中
    #（真 zod 本体形；无进仓 main.sai，--check 同判）。
    if [ -f "$d/expect.refused" ]; then
      if "$TSGO_BIN" build --out "$out/ws" "$d" >"$out/tsgo.log" 2>&1; then
        echo "$n FAIL expected refusal but build passed"
        return 0
      fi
      while IFS= read -r fp || [ -n "$fp" ]; do
        case "$fp" in ""|\#*) continue ;; esac
        if ! grep -qF "$fp" "$out/tsgo.log"; then
          echo "$n FAIL refusal fingerprint missing: $fp"
          return 0
        fi
      done < "$d/expect.refused"
      echo "$n PASS refused-as-expected"
      return 0
    fi
    if ! "$TSGO_BIN" build --out "$out/ws" "$d" >"$out/tsgo.log" 2>&1; then
      echo "$n FAIL tsgo build refused: $(grep -m1 diag "$out/tsgo.log" | head -c 160)"
      return 0
    fi
    MERGED=$(ls "$out"/ws/packages/*/src/main.sai 2>/dev/null | head -1)
    if [ -z "$MERGED" ]; then echo "$n FAIL no merged main.sai"; return 0; fi
    if [ "$CHECK" -eq 1 ]; then
      if ! diff -q "$d/main.sai" "$MERGED" >"$out/diff.txt" 2>&1; then
        echo "$n FAIL main.sai drift (hand edit or compiler change)"
        return 0
      fi
      echo "$n SA-CLEAN"
      return 0
    fi
    cp "$MERGED" "$d/main.sai"
    SAI="$d/main.sai"
  else
  if ! "$TSGO_BIN" --sa --out "$SA_OUT" "$d/main.ts" >"$out/tsgo.log" 2>&1; then
    if grep -q "error:" "$out/tsgo.log" 2>/dev/null; then detail="tsgo error"; else detail="transpile refused"; fi
    echo "$n FAIL $detail: $(cat "$SA_OUT"/subset-report.txt 2>/dev/null | head -1)"
    return 0
  fi
  if [ "$CHECK" -eq 1 ]; then
    if ! diff -q "$d/main.sai" "$out/main.sai" >"$out/diff.txt" 2>&1; then
      echo "$n FAIL main.sai drift (hand edit or compiler change)"
      return 0
    fi
    echo "$n SA-CLEAN"
    return 0
  fi
  SAI="$d/main.sai"
  fi
  # 插件 demo（$d/sa.mod 进仓固定装置）：--project-root 供 bare node.sai 解析。
  if [ -f "$d/sa.mod" ]; then
    BUILD_OK=0
    "$SA_BIN" build-exe --project-root "$d" "$SAI" -o "$out/demo" >"$out/build.log" 2>&1 || BUILD_OK=$?
  else
    BUILD_OK=0
    "$SA_BIN" build-exe "$SAI" -o "$out/demo" >"$out/build.log" 2>&1 || BUILD_OK=$?
  fi
  if [ "$BUILD_OK" -ne 0 ]; then
    echo "$n FAIL sa build-exe failed: $(head -c 200 "$out/build.log")"
    return 0
  fi
  if ! "$out/demo" >"$out/actual.stdout" 2>"$out/run.stderr"; then
    echo "$n FAIL run exit=$?: $(head -c 120 "$out/run.stderr")"
    return 0
  fi
  if ! diff -u "$d/expected.stdout" "$out/actual.stdout" >"$out/diff.txt" 2>&1; then
    echo "$n FAIL stdout mismatch"
    return 0
  fi
  echo "$n PASS"
}
export HERE CHECK TSGO_BIN SA_BIN
export -f run_one_demo 2>/dev/null || true

tmp_res=$(mktemp -d)
# shellcheck disable=SC2064
trap "rm -rf '$tmp_res'" EXIT
# shellcheck disable=SC2086
echo "$DEMOS" | tr ' ' '\n' | grep -v '^$' | xargs -P "$JOBS" -I{} bash -c 'run_one_demo "$@" >"'"$tmp_res"'/$1.out" 2>&1' _ {} || true
pass=0; fail=0; failed=""
printf "%-22s %-10s %s\n" "DEMO" "STATUS" "DETAIL"
for n in $DEMOS; do
  line=$(cat "$tmp_res/$n.out" 2>/dev/null || echo "$n MISSING worker failed")
  status=$(echo "$line" | awk '{print $2}')
  detail=$(echo "$line" | cut -d' ' -f3-)
  case "$status" in
    PASS|SA-CLEAN) printf "%-22s %-10s %s\n" "$n" "$status" ""; pass=$((pass+1)) ;;
    SKIP) printf "%-22s %-10s %s\n" "$n" "$status" "$detail" ;;
    *) printf "%-22s %-10s %s\n" "$n" "FAIL" "$detail"; fail=$((fail+1)); failed="$failed $n" ;;
  esac
done
echo "== pass=$pass fail=$fail =="
[ -n "$failed" ] && echo "failed:$failed"
[ "$fail" -eq 0 ]
