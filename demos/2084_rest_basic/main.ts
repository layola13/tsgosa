function main(): i32 {
  const a: number[] = [1, 2, 3, 4];
  const [x, ...rest] = a;
  console.log(x);
  console.log(rest[0]);
  console.log(rest.length);
  const [y] = rest;
  console.log(y);
  return 0;
}
