function main(): i32 {
  const m = new Map<i32, i32[]>();
  m.set(1, [10, 20]);
  console.log(m.get(1)?.[1] ?? -1);
  return 0;
}
