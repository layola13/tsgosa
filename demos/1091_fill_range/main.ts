function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  a.fill(9, 1, 3);
  console.log(a[0]);
  console.log(a[1]);
  console.log(a[3]);
  return 0;
}
