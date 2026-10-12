function main(): i32 {
  const [x = [9]] = [[1]];
  console.log(x[0]);
  return 0;
}
