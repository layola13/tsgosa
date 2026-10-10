function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  m.set(2, 20);
  console.log(m.values().length);
  console.log(m.keys().length);
  return 0;
}
