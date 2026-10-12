function main(): i32 {
  const nn: number[][] = [[1], [2], [3]];
  const [x, ...rest] = nn;
  console.log(x[0]);
  const [y] = rest;
  console.log(y[0]);
  console.log(rest.length);
  return 0;
}
