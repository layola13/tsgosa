function main(): i32 {
  const a: i32[] = [3, 1, 2];
  const b: i32[] = a.toSorted();
  console.log(b[0]);
  console.log(a[0]);
  return 0;
}
