function main(): i32 {
  const a: number[] = [3, 1, 2];
  const b: number[] = a.toSorted();
  console.log(b[0], b[1], b[2], a[0]);
  return 0;
}
