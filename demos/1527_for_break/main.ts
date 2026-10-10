function main(): i32 {
  let s = 0;
  for (let i = 0; i < 10; i = i + 1) {
    if (i >= 3) { break; }
    s = s + i;
  }
  console.log(s);
  return 0;
}
