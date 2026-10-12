function main(): i32 {
  const a: number[] = [1];
  const [x = 9, ...rest] = a;
  console.log(x);
  console.log(rest.length);
  return 0;
}
