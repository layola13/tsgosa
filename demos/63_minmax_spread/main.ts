function main(): i32 {
  const a: number[] = [3, 1, 4];
  console.log(Math.max(...a) + Math.min(...a));
  return 0;
}
