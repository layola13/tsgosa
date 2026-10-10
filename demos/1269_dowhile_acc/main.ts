function main(): i32 {
  let s: i32 = 0;
  let i: i32 = 0;
  do {
    s = s + i;
    i = i + 1;
  } while (i < 5);
  console.log(s);
  return 0;
}
