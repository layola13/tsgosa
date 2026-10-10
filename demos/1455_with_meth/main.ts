function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const b: i32[] = a.with(0, 9);
  console.log(b[0]);
  console.log(a[0]);
  return 0;
}
