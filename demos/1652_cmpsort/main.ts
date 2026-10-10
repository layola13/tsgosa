function main(): i32 {
  const a: i32[] = [6, 2, 9, 1];
  a.sort((x, y) => x - y);
  console.log(a[0]);
  return 0;
}
