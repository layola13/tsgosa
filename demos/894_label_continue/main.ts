function main(): i32 {
  let t = 0;
  outer: for (let i = 0; i < 4; i++) {
    for (let j = 0; j < 4; j++) {
      if (j === 2) { continue outer; }
      t += 1;
    }
  }
  console.log(t);
  return 0;
}
