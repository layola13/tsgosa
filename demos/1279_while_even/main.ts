function main(): i32 {
  let s: i32 = 0;
  let i: i32 = 0;
  while (i < 8) {
    i = i + 2;
    s = s + i;
  }
  console.log(s);
  return 0;
}
