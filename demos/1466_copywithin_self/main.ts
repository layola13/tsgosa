function main(): i32 {
  const a: i32[] = [1, 2];
  a.copyWithin(0, 0);
  console.log(a[0]);
  console.log(a[1]);
  return 0;
}
