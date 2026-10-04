function main(): i32 {
  const m: number[][] = [[1, 2], [3, 4]];
  let t: i32 = 0;
  for (const row of m) {
    t = t + row[0];
  }
  console.log(t);
  return 0;
}