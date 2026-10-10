function main(): i32 {
  let s: i32 = 0;
  let i: i32 = 0;
  do {
    i = i + 1;
    if (i == 2) { continue; }
    s = s + i;
  } while (i < 4);
  console.log(s);
  return 0;
}
