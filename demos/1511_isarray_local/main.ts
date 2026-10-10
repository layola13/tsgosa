function main(): i32 {
  const a: i32[] = [1, 2];
  console.log(Array.isArray(a) ? 1 : 0);
  const n: i32 = 5;
  console.log(Array.isArray(n) ? 1 : 0);
  return 0;
}
