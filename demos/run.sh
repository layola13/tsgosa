#!/bin/bash
# tsgosa/demos/run.sh — TS 特性进度 runner: main.ts -> tsgo --sa -> main.sai -> sa run -> diff expected.stdout
# 用法: ./run.sh [demo目录名...]   (无参数=全量)
set -u
set -o pipefail
export PATH=$PATH:/opt/zig:/usr/local/go/bin

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
  # .sai 落 demo 根（随批进仓供检查）；其余生成物进 out/（忽略）
  if ! "$TSGO_BIN" --sa --out "$d" "$d/main.ts" >"$out/tsgo.log" 2>&1; then
    if grep -q "error:" "$out/tsgo.log" 2>/dev/null; then detail="tsgo error"; else detail="transpile refused"; fi
    printf "%-22s %-10s %s\n" "$n" "FAIL" "$detail: $(cat "$d"/subset-report.txt 2>/dev/null | head -1)"
    fail=$((fail+1)); failed="$failed $n"; continue
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
