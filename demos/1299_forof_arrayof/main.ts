function main(): i32 {
  let s: i32 = 0;
  for (const v of Array.of(4, 5)) {
    s = s + v;
  }
  console.log(s);
  return 0;
}
