function main(): i32 {
  const m = new Map<number, number>();
  m.set(1, 7);
  const f: boolean = false;
  const k: number = 1;
  console.log(f == m[k]);
  return 0;
}
