function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  m.set(2, 20);
  const a = m.keys().length;
  const b = m.keys().length;
  console.log(a);
  console.log(b);
  return 0;
}
