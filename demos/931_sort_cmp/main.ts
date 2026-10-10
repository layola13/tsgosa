function main(): i32 {
  const a: i32[] = [3, 1, 2];
  a.sort((x: i32, y: i32) => x - y);
  console.log(a[0]);
  console.log(a[2]);
  return 0;
}
