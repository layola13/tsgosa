function main(): i32 {
  const a: number[][] = [[1, 2], [3, 4]];
  const b: number[][] = [[2, 0], [1, 2]];
  let t00: i32 = 0;
  let t11: i32 = 0;
  for (let k: i32 = 0; k < 2; k++) {
    t00 = t00 + a[0][k] * b[k][0];
    t11 = t11 + a[1][k] * b[k][1];
  }
  console.log(t00, t11);
  return 0;
}