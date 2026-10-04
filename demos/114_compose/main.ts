function probe(): i32 {
  const a: number[] = [1, 2, 3];
  const b: number[] = a.map((x: i32) => x + 1).filter((x: i32) => x > 2);
  return b.length + b[0];
}
function main(): i32 {
  console.log(probe());
  return 0;
}
