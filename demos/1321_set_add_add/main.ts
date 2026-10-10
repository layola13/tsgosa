function main(): i32 {
  const s = new Set<i32>();
  s.add(1).add(2);
  console.log(s.size);
  console.log(s.has(2) ? 1 : 0);
  return 0;
}
