function main(): i32 {
  const m = new Map<number, number>();
  m.set(1, 7);
  const f: boolean = false;
  const t: boolean = true;
  console.log(f == m.get(1));
  console.log(t == m.get(2));
  console.log(t != m.get(1));
  return 0;
}
