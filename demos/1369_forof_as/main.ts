function main(): i32 {
  const a: i32[] = [1, 2, 3];
  let s: i32 = 0;
  for (const v of a as i32[]) {
    s = s + v;
  }
  console.log(s);
  return 0;
}
