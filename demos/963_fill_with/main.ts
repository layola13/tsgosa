function main(): i32 {
  const a: i32[] = [1, 2, 3];
  a.fill(9);
  console.log(a[1]);
  console.log(a.with(0, 7)[0]);
  return 0;
}
