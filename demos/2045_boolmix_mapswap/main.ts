function main(): i32 {
  const m = new Map<number, number>();
  m.set(1, 7);
  const f: boolean = false;
  console.log(m.get(1) == f);
  console.log(m.get(2) != f);
  return 0;
}
