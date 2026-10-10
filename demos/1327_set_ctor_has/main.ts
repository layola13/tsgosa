function main(): i32 {
  const s = new Set<i32>([1, 2]);
  console.log(s.has(1) ? 1 : 0);
  return 0;
}
