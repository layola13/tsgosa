function main(): i32 {
  const aa = [[1]];
  const [[a = 5]] = aa;
  console.log(a);
  return 0;
}
