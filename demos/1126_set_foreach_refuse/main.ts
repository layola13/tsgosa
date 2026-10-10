function main(): i32 {
  const s = new Set<i32>([3, 1, 2]);
  let t = 0;
  s.forEach((v: i32) => { t += v; });
  console.log(t);
  console.log(s.size);
  return 0;
}
