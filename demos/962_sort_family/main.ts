function main(): i32 {
  const a: i32[] = [3, 1, 2];
  a.sort();
  console.log(a[0]);
  console.log(a.toSorted()[2]);
  return 0;
}
