function main(): i32 {
  const nn: number[][] = [[1, 2], [3]];
  const [x = ["a"]] = nn;
  console.log(x[0]);
  return 0;
}
