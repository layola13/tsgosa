function g(nn: number[][]): number {
  const [x = [9], z = []] = nn;
  return x[0] + z.length;
}
function main(): i32 {
  console.log(g([[1, 2], [3]]));
  console.log(g([[1]]));
  return 0;
}
