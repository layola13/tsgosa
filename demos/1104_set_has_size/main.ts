function main(): i32 {
  const s = new Set<i32>();
  s.add(5);
  console.log(s.has(5) ? 1 : 0);
  console.log(s.has(6) ? 1 : 0);
  console.log(s.size);
  return 0;
}
