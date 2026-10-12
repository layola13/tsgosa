function g(nn: number[][]): number {
  const [x = [9], y = [8]] = nn;
  return x[0] + y[0];
}
function main(): i32 {
  console.log(g([[1, 2], [3]]));
  console.log(g([[1]]));
  return 0;
}
