function main(): i32 {
  const a: number[][] = [[1, 2], [3, 4]];
  const b: number[][] = [[5, 6], [7, 8]];
  let t: i32 = 0;
  for (let i: i32 = 0; i < 2; i++) {
    for (let j: i32 = 0; j < 2; j++) {
      t = t + a[i][j] + b[i][j];
    }
  }
  console.log(t);
  return 0;
}