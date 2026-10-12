function main(): i32 {
  const nn: number[][] = [[1, 2], [3]];
  const [x = [9]] = nn;
  console.log(x[0]);
  console.log(x[1]);
  return 0;
}
