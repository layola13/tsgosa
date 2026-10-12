function main(): i32 {
  const a: number[] = [1, 2];
  const [...[x]] = a;
  console.log(x);
  return 0;
}
