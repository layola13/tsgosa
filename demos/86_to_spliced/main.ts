function main(): i32 {
  const a: number[] = [1, 2, 3, 4];
  const b: number[] = a.toSpliced(1, 2);
  console.log(b[0], b[1], b.length);
  return 0;
}
