function main(): i32 {
  let s: i32 = 0;
  for (const v of new Array<i32>(3).fill(5)) {
    s = s + v;
  }
  console.log(s);
  return 0;
}
