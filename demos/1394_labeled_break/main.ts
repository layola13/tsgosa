function main(): i32 {
  let s: i32 = 0;
  outer: for (let i: i32 = 0; i < 3; i = i + 1) {
    for (let j: i32 = 0; j < 3; j = j + 1) {
      if (j == 1) { break outer; }
      s = s + 1;
    }
  }
  console.log(s);
  return 0;
}
