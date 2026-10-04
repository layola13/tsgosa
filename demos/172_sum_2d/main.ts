function main(): i32 {
  const m: number[][] = [[1, 2, 3], [4, 5, 6]];
  let t: i32 = 0;
  for (let i: i32 = 0; i < 2; i++) {
    for (let j: i32 = 0; j < 3; j++) {
      t = t + m[i][j];
    }
  }
  console.log(t);
  return 0;
}