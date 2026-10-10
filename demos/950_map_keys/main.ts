function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  m.set(2, 20);
  let t = 0;
  for (const k of m.keys()) { t += k; }
  console.log(t);
  return 0;
}
