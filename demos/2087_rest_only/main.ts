function main(): i32 {
  const a: number[] = [1, 2, 3];
  const [...rest] = a;
  console.log(rest.length);
  return 0;
}
