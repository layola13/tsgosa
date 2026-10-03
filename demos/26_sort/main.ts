function main(): i32 {
  const a: number[] = [3, 1, 4, 1, 5];
  a.sort();
  console.log(a[0] + a[4]);
  const b: number[] = [9, 2, 7];
  const c = b.toSorted();
  console.log(c[0] + c[2]);
  console.log(b[0]);
  return 0;
}
