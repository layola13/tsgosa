function main(): i32 {
  const a: number[] = [1, 2, 3];
  const b: number[] = a.with(1, 9);
  console.log(b[0], b[1], b[2], a[1]);
  return 0;
}
