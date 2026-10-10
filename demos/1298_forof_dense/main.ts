function main(): i32 {
  let s: i32 = 0;
  for (const v of new Array<i32>(1, 2, 3)) {
    s = s + v;
  }
  console.log(s);
  return 0;
}
