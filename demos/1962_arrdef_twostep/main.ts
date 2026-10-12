function g(nn: number[][]): number {
  const [x = [9]] = nn;
  const [y] = x;
  return y;
}
function main(): i32 {
  console.log(g([[4, 5]]));
  return 0;
}
