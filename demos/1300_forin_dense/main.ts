function main(): i32 {
  let s: i32 = 0;
  for (const k in new Array<i32>(7, 8)) {
    s = s + 1;
  }
  console.log(s);
  return 0;
}
