function main(): i32 {
  const a: number[] = [1, 2];
  const [...rest, x] = a;
  console.log(x);
  return 0;
}
