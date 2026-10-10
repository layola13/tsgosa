function main(): i32 {
  let t = 0;
  outer: for (let i = 0; i < 5; i++) {
    for (let j = 0; j < 5; j++) {
      if (j === 3) { break outer; }
      t += 1;
    }
  }
  console.log(t);
  return 0;
}
