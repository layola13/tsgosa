function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  console.log(m.has(1) ? 1 : 0);
  console.log(m.has(2) ? 1 : 0);
  console.log(m.get(2) ?? -1);
  return 0;
}
