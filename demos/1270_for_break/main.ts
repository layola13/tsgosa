function main(): i32 {
  let s: i32 = 0;
  for (let i: i32 = 0; i < 10; i = i + 1) {
    if (i == 5) { break; }
    s = s + i;
  }
  console.log(s);
  return 0;
}
