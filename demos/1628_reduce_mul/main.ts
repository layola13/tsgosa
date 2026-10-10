function main(): i32 {
  const a: i32[] = [1, 2, 3];
  console.log(a.reduce((x, y) => x * y, 1));
  return 0;
}
