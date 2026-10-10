function main(): i32 {
  let s: i32 = 0;
  for (let i: i32 = 0; i < 3; i = i + 1) {
    for (let j: i32 = 0; j < 4; j = j + 1) {
      s = s + 1;
    }
  }
  console.log(s);
  return 0;
}
