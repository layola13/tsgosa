function main(): i32 {
  let s: i32 = 0;
  for (let i: i32 = 10; i > 0; i = i - 2) {
    s = s + i;
  }
  console.log(s);
  return 0;
}
