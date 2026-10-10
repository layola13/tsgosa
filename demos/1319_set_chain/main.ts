function main(): i32 {
  const s = new Set<i32>();
  console.log(s.add(5).has(5) ? 1 : 0);
  console.log(s.size);
  return 0;
}
