function main(): i32 {
  const s = new Set<i32>();
  s.add(1);
  s.add(2);
  s.clear();
  console.log(s.size);
  console.log(s.has(1) ? 1 : 0);
  s.add(9);
  console.log(s.size);
  return 0;
}
