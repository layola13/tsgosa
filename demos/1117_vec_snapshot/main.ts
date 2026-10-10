function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  const n = m.keys().length;
  m.set(2, 20);
  console.log(n);
  console.log(m.keys().length);
  return 0;
}
