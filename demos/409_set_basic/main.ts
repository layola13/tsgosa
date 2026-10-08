function main(): i32 {
  const s = new Set<i32>();
  s.add(1);
  s.add(2);
  console.log(s.size + (s.has(1) ? 10 : 0));
  return 0;
}
