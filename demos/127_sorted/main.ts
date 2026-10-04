function main(): i32 {
  const a: number[] = [1, 2, 3, 4];
  console.log(a.every((x: i32) => x > 0));
  const b: number[] = [1, -2, 3];
  console.log(b.every((x: i32) => x > 0));
  return 0;
}
