function main(): i32 {
  const a: number[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
  const e: number[] = a.filter((x: i32) => x % 2 == 0);
  let t: i32 = 0;
  for (const x of e) {
    t = t + x;
  }
  console.log(e.length, t);
  return 0;
}