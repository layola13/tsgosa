function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  let t = 0;
  for (const [k, v] of m.entries()) { t += k + v; }
  console.log(t);
  return 0;
}
