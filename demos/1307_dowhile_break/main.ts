function main(): i32 {
  let s: i32 = 0;
  let i: i32 = 0;
  do {
    i = i + 1;
    if (i == 3) { break; }
    s = s + i;
  } while (i < 10);
  console.log(s);
  return 0;
}
