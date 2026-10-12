function g(nn: number[][]): number {
  const [x = [[5]]] = nn;
  return x[0][0];
}
function main(): i32 {
  console.log(g([[[1]]]));
  return 0;
}
