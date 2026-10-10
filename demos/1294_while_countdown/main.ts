function main(): i32 {
  let s: i32 = 0;
  let i: i32 = 5;
  while (i >= 0) {
    s = s + i;
    i = i - 1;
  }
  console.log(s);
  return 0;
}
