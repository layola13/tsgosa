function main(): i32 {
  const a: number[] = [1, 2, 3, 4];
  const b: number[] = a.slice(1);
  console.log(b.length, b[0]);
  return 0;
}