function main(): i32 {
  const m = new Map<i32, i32>();
  m.set(1, 10);
  m.set(2, 20);
  let t = 0;
  m.forEach((v: i32) => { t += v; });
  console.log(t);
  return 0;
}
