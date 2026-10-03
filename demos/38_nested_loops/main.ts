function main(): i32 {
  let t: i32 = 0;
  for (let i: i32 = 0; i < 3; i++) {
    for (let j: i32 = 0; j < 3; j++) {
      if (j == 1) {
        continue;
      }
      t = t + i * 10 + j;
    }
  }
  console.log(t);
  return 0;
}
