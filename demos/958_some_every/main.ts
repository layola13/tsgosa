function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  console.log(a.some((v: i32) => v > 3) ? 1 : 0);
  console.log(a.every((v: i32) => v > 0) ? 1 : 0);
  return 0;
}
