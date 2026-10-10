function main(): i32 {
  const s = new Set<i32>();
  s.add(1);
  s.add(2);
  console.log(s.delete(1) ? 1 : 0);
  console.log(s.delete(9) ? 1 : 0);
  console.log(s.size);
  return 0;
}
