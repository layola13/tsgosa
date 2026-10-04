function main(): i32 {
  const a: number[] = [1, 2, 3, 4, 5, 6];
  const e: number[] = a.filter((x: i32) => x % 2 == 0);
  console.log(e.length, e[0] + e[1]);
  return 0;
}
