#!/bin/bash
# tsgosa/demos/run.sh — TS 特性进度 runner: main.ts -> tsgo --sa -> main.sai -> sa build-exe -> diff expected.stdout
# 用法: ./run.sh [--check] [demo目录名...]   (无参数=全量)
# --check: 只重生成 .sai 并与进仓版逐字节比对(.sai 禁止手改,只能由编译器出;供 CI/提交前自证)
set -u
set -o pipefail
export PATH=$PATH:/opt/zig:/usr/local/go/bin

CHECK=0
if [ "${1:-}" = "--check" ]; then
  CHECK=1
  shift
fi

HERE=$(dirname "$(realpath "$0")")
TSGOSA=$(dirname "$HERE")
TSGO_BIN=${TSGO_BIN:-/tmp/tsgo-demo}
SA_BIN=${SA_BIN:-/content/sa_all/sci/zig-out/bin/sa}

if [ ! -x "$TSGO_BIN" ]; then
  echo "building tsgo ..."
  (cd "$TSGOSA" && go build -o "$TSGO_BIN" ./cmd/tsgo) || exit 2
fi
if [ ! -x "$SA_BIN" ]; then
  echo "error: sa backend not found at $SA_BIN (build sci: zig build -Dllvm=false)" >&2
  exit 2
fi

if [ "$#" -gt 0 ]; then
  DEMOS="$*"
else
  DEMOS=$(ls "$HERE")
fi

pass=0; fail=0; failed=""
printf "%-22s %-10s %s\n" "DEMO" "STATUS" "DETAIL"
for n in $DEMOS; do
  d="$HERE/$n"
  [ -f "$d/main.ts" ] || continue
  [ -f "$d/expected.stdout" ] || { printf "%-22s %-10s %s\n" "$n" "SKIP" "no expected.stdout"; continue; }
  out="$d/out"
  rm -rf "$out"
  mkdir -p "$out"
  # .sai 落 demo 根（随批进仓供检查；其余生成物进 out/（忽略）
  # --check 模式: 生成到临时位,只与进仓 main.sai 比对,不碰工作区
  if [ "$CHECK" -eq 1 ]; then
    out="$out/check.$$"
    mkdir -p "$out"
    SA_OUT="$out/main.sai"
  else
    SA_OUT="$d"
  fi
  if ! "$TSGO_BIN" --sa --out "$SA_OUT" "$d/main.ts" >"$out/tsgo.log" 2>&1; then
    if grep -q "error:" "$out/tsgo.log" 2>/dev/null; then detail="tsgo error"; else detail="transpile refused"; fi
    printf "%-22s %-10s %s\n" "$n" "FAIL" "$detail: $(cat "$SA_OUT"/subset-report.txt 2>/dev/null | head -1)"
    fail=$((fail+1)); failed="$failed $n"; continue
  fi
  if [ "$CHECK" -eq 1 ]; then
    if ! diff -q "$d/main.sai" "$out/main.sai" >"$out/diff.txt" 2>&1; then
      printf "%-22s %-10s %s\n" "$n" "FAIL" "main.sai drift (hand edit or compiler change)"
      fail=$((fail+1)); failed="$failed $n"; continue
    fi
    printf "%-22s %-10s\n" "$n" "SA-CLEAN"
    pass=$((pass+1)); continue
  fi
  if ! "$SA_BIN" build-exe "$d/main.sai" -o "$out/demo" >"$out/build.log" 2>&1; then
    printf "%-22s %-10s %s\n" "$n" "FAIL" "sa build-exe failed: $(head -c 200 "$out/build.log")"
    fail=$((fail+1)); failed="$failed $n"; continue
  fi
  if ! "$out/demo" >"$out/actual.stdout" 2>"$out/run.stderr"; then
    printf "%-22s %-10s %s\n" "$n" "FAIL" "run exit=$?: $(head -c 120 "$out/run.stderr")"
    fail=$((fail+1)); failed="$failed $n"; continue
  fi
  if ! diff -u "$d/expected.stdout" "$out/actual.stdout" >"$out/diff.txt" 2>&1; then
    printf "%-22s %-10s %s\n" "$n" "FAIL" "stdout mismatch"
    fail=$((fail+1)); failed="$failed $n"; continue
  fi
  printf "%-22s %-10s\n" "$n" "PASS"
  pass=$((pass+1))
done
echo "== pass=$pass fail=$fail =="
[ -n "$failed" ] && echo "failed:$failed"
[ "$fail" -eq 0 ]
