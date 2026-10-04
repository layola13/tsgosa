function main(): i32 {
  const a: number[] = [7, 1, 5, 3, 9];
  const b: number[] = a.toSorted();
  console.log(b[2], b[0] + b[4]);
  return 0;
}