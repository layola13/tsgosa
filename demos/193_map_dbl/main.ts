function main(): i32 {
  const a: number[] = [1, 2, 3];
  const b: number[] = a.map((x: i32) => x * 2);
  console.log(b[0], b[1], b[2]);
  return 0;
}