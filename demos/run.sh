#!/bin/bash
# tsgosa/demos/run.sh — TS 特性进度 runner: main.ts -> tsgo --sa -> main.sai -> sa build-exe -> diff expected.stdout
# 用法: ./run.sh [-j N] [--check] [demo目录名...]   (无参数=全量)
#   -j N / --jobs N: 并行跑 N 例（默认 CPU 数；输出表仍按目录序打印，PASS/FAIL 与串行一致）
#   --corpus: 对 sa_plugin_ts/demos 语料跑上游/薄口通拒差分（零分歧即过；同样受 -j 控制）
#   --parity: 对 program demo（package.json 工程）跑上游 build/薄口 build 通拒对照表
#     （只报告 UP_EXIT/TN_EXIT，不判 PASS/FAIL；有意分歧见 AGENTS step172 矩阵；
#     上游不可用即 SKIP，不卡门）
#   --corpus-sai: 同通过例归一化比 .sai 逐行（去头注释/缩进/return-ret 方言；
#     只报告，不卡门；分歧例人工审是否为方言外差异）
# --check: 只重生成 .sai 并与进仓版逐字节比对(.sai 禁止手改,只能由编译器出;供 CI/提交前自证)
set -u
set -o pipefail
export PATH=$PATH:/opt/zig:/usr/local/go/bin

JOBS=0
CHECK=0
CORPUS=0
CORPUSSAI=0
PARITY=0
while [ "$#" -gt 0 ]; do
  case "${1:-}" in
    --check) CHECK=1; shift ;;
    --corpus) CORPUS=1; shift ;;
    --corpus-sai) CORPUSSAI=1; shift ;;
    --parity) PARITY=1; shift ;;
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

# ---- corpus-sai 模式: 同通过例归一化比 .sai（只报告，不卡门）----
if [ "$CORPUSSAI" -eq 1 ]; then
  SATSGO_DIR=${SATSGO_DIR:-/content/sa_all/satsgo}
  CORPUS_DIR=${CORPUS_DIR:-/content/sa_all/sa_plugin_ts/demos}
  UP_BIN=${UP_BIN:-/tmp/tsgo-sa-upstream}
  if [ -n "$(find "$SATSGO_DIR/cmd/tsgo-sa" "$SATSGO_DIR/internal/saemit" -name '*.go' -newer "$UP_BIN" 2>/dev/null | head -1)" ] || [ ! -x "$UP_BIN" ]; then
    echo "building upstream tsgo-sa ..."
    (cd "$SATSGO_DIR" && go build -o "$UP_BIN" ./cmd/tsgo-sa) || exit 2
  fi
  [ -d "$CORPUS_DIR" ] || { echo "error: corpus not found at $CORPUS_DIR" >&2; exit 2; }
  # 归一化：删头注释行/空行/前导空白，return→ret（已认可方言差）。
  norm_sai() {
    grep -v "^//" "$1" 2>/dev/null | sed -e 's/^[[:space:]]*//' -e 's/^return /ret /' | grep -v "^$" || true
  }
  run_one_sai() {
    d="$1"
    f="$CORPUS_DIR/$d"
    src=""
    [ -f "$f/main.ts" ] && src="$f/main.ts"
    [ -z "$src" ] && return 0
    base=$(basename "$src" .ts)
    o1=$(mktemp -d); o2=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$o1' '$o2'" RETURN
    "$UP_BIN" --out "$o1" "$src" >/dev/null 2>&1
    up_ok=1; grep -q "refused=true" "$o1/subset-report.txt" 2>/dev/null && up_ok=0
    "$TSGO_BIN" --sa --out "$o2" "$src" >/dev/null 2>&1
    port_ok=1
    if [ -f "$o2/$base.sai" ] && ! grep -qv "warning:" "$o2/subset-report.txt" 2>/dev/null; then port_ok=1; else port_ok=0; fi
    [ "$up_ok" -eq 1 ] && [ "$port_ok" -eq 1 ] || { echo "$d SKIP-REFUSED"; return 0; }
    up_sai=$(ls "$o1"/src/*.sai "$o1"/*.sai 2>/dev/null | head -1)
    [ -n "$up_sai" ] || { echo "$d NO-UP-SAI"; return 0; }
    if diff <(norm_sai "$up_sai") <(norm_sai "$o2/$base.sai") >/dev/null 2>&1; then
      echo "$d SAI-SAME"
    else
      n=$(diff <(norm_sai "$up_sai") <(norm_sai "$o2/$base.sai") 2>/dev/null | grep -c "^[<>]")
      echo "$d SAI-DIFF lines=$n"
    fi
  }
  export TSGO_BIN UP_BIN CORPUS_DIR
  export -f run_one_sai norm_sai 2>/dev/null || true
  tmp_sai=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp_sai'" EXIT
  names=$(ls "$CORPUS_DIR")
  # shellcheck disable=SC2086
  echo "$names" | xargs -P "$JOBS" -I{} bash -c 'run_one_sai "$@" >"'"$tmp_sai"'/$1.out" 2>&1' _ {} || true
  same=0; diffn=0; skip=0; bad=""; big=""
  for d in $names; do
    line=$(cat "$tmp_sai/$d.out" 2>/dev/null || echo "$d MISSING")
    case "$line" in
      *" SAI-SAME"*) same=$((same+1)) ;;
      *" SAI-DIFF lines="*)
        diffn=$((diffn+1)); bad="$bad $d"
        n=${line##*lines=}; n=${n%% *}
        case "$n" in ''|*[!0-9]*) n=0 ;; esac
        [ "$n" -gt 20 ] && big="$big $d($n)"
        ;;
      *) skip=$((skip+1)) ;;
    esac
  done
  echo "== sai-same=$same sai-diff=$diffn skipped=$skip =="
  [ -n "$big" ] && echo "big-diff(>20):$big"
  [ -n "$bad" ] && echo "diff-cases:$bad"
  exit 0
fi

# ---- parity 模式: program demo 上游 build vs 薄口 build 通拒对照（只报告）----
if [ "$PARITY" -eq 1 ]; then
  SATSGO_DIR=${SATSGO_DIR:-/content/sa_all/satsgo}
  UP_BIN=${UP_BIN:-/tmp/tsgo-sa-upstream}
  if [ ! -d "$SATSGO_DIR/cmd/tsgo-sa" ]; then
    echo "parity SKIP: upstream not found at $SATSGO_DIR"
    exit 0
  fi
  if [ -n "$(find "$SATSGO_DIR/cmd/tsgo-sa" "$SATSGO_DIR/internal/saemit" -name '*.go' -newer "$UP_BIN" 2>/dev/null | head -1)" ] || [ ! -x "$UP_BIN" ]; then
    echo "building upstream tsgo-sa ..."
    (cd "$SATSGO_DIR" && go build -o "$UP_BIN" ./cmd/tsgo-sa) || { echo "parity SKIP: upstream build failed"; exit 0; }
  fi
  run_one_parity() {
    n="$1"
    d="$HERE/$n"
    [ -f "$d/package.json" ] || return 0
    # demo 内 out/ 与上游残留不进上游输入：拷到临时干净树（node_modules fixture 同步带走）。
    t=$(mktemp -d)
    for f in "$d"/*; do
      b=$(basename "$f")
      case "$b" in out|*.sa) continue ;; esac
      cp -r "$f" "$t/" 2>/dev/null
    done
    uo=$(mktemp -d); to=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$t' '$uo' '$to'" RETURN
    (cd "$t" && "$UP_BIN" build --out "$uo" . >/dev/null 2>&1); up=$?
    (cd "$t" && "$TSGO_BIN" build --out "$to" . >/dev/null 2>&1); tn=$?
    if [ "$up" -eq "$tn" ]; then tag="SAME"; else tag="DIVERGED"; fi
    echo "$n $tag up=$up tn=$tn"
  }
  if [ "$#" -gt 0 ]; then
    DEMOS=$(printf '%s\n' "$@")
  else
    DEMOS=$(ls "$HERE")
  fi
  FILTERED=""
  for n in $DEMOS; do
    [ -f "$HERE/$n/main.ts" ] || continue
    [ -f "$HERE/$n/package.json" ] || continue
    FILTERED="$FILTERED
$n"
  done
  DEMOS="$FILTERED"
  export TSGO_BIN UP_BIN HERE
  export -f run_one_parity 2>/dev/null || true
  tmp_par=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp_par'" EXIT
  # shellcheck disable=SC2086
  echo "$DEMOS" | xargs -P "$JOBS" -I{} bash -c 'run_one_parity "$@" >"'"$tmp_par"'/$1.out" 2>&1' _ {} || true
  printf "%-20s %-9s %s\n" DEMO VERDICT DETAIL
  for n in $DEMOS; do
    line=$(cat "$tmp_par/$n.out" 2>/dev/null || echo "$n MISSING")
    # 行形：`<demo> SAME|DIVERGED up=<u> tn=<t>`
    set -- $line
    printf "%-20s %-9s %s\n" "$1" "$2" "$3 $4"
  done
  exit 0
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
  SAI_ROOT=""
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
    # workspace，分裂布局（成员多 .sa，入口 main.sa 经 ./x.sa 跨文件引用；
    # sla workspace 真形态）。进仓 $d/*.sa 全单元（--check 逐个比对）。
    # 跑分用 ws 内副本 + --project-root（相对 @import 解析）。
    # 预期拒收工程（$d/expect.refused，每行一指纹）：build 必须拒收且指纹全中
    #（真 zod 本体形；无进仓 .sa，--check 同判）。
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
    MERGED=$(ls "$out"/ws/packages/*/src/main.sa 2>/dev/null | head -1)
    if [ -z "$MERGED" ]; then echo "$n FAIL no merged main.sa"; return 0; fi
    # 分裂布局：成员全部 .sa 进仓（$d/<unit>.sa），逐字节比对，防合并回退。
    WSSRC=$(dirname "$MERGED")
    if [ "$CHECK" -eq 1 ]; then
      for f in "$WSSRC"/*.sa; do
        u=$(basename "$f")
        if ! diff -q "$d/$u" "$f" >"$out/diff.txt" 2>&1; then
          echo "$n FAIL $u drift (hand edit or compiler change)"
          return 0
        fi
      done
      # 进仓多余 .sa（已删单元）亦报。
      for f in "$d"/*.sa; do
        [ -e "$f" ] || continue
        u=$(basename "$f")
        if [ ! -f "$WSSRC/$u" ]; then
          echo "$n FAIL stale $u (unit removed)"
          return 0
        fi
      done
      echo "$n SA-CLEAN"
      return 0
    fi
    cp "$WSSRC"/*.sa "$d/"
    SAI="$MERGED"
    SAI_ROOT="$out/ws"
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
  # program demo：SAI_ROOT 置 ws 根，解相对 ./x.sa。
  if [ -n "${SAI_ROOT:-}" ]; then
    BUILD_OK=0
    "$SA_BIN" build-exe --project-root "$SAI_ROOT" "$SAI" -o "$out/demo" >"$out/build.log" 2>&1 || BUILD_OK=$?
  elif [ -f "$d/sa.mod" ]; then
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
