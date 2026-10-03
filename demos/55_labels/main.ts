function main(): i32 {
  let t: i32 = 0;
  outer: for (let i: i32 = 0; i < 3; i++) {
    for (let j: i32 = 0; j < 3; j++) {
      if (j == 1) {
        continue outer;
      }
      t = t + 1;
    }
  }
  console.log(t);
  return 0;
}
