function main(): i32 {
  let s: i32 = 0;
  for (const v of Array(1, 2)) {
    s = s + v;
  }
  console.log(s);
  return 0;
}
