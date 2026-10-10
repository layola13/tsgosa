function main(): i32 {
  let i: i32 = 0;
  for (;;) {
    i = i + 1;
    if (i >= 3) { break; }
  }
  console.log(i);
  return 0;
}
