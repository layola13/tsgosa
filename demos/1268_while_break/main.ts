function main(): i32 {
  let s: i32 = 0;
  let i: i32 = 0;
  while (i < 10) {
    i = i + 1;
    if (i == 4) { continue; }
    if (i == 7) { break; }
    s = s + i;
  }
  console.log(s);
  return 0;
}
