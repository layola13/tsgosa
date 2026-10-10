function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  let s: i32 = 0;
  for (const v of a) {
    s = s + v;
  }
  console.log(s);
  return 0;
}
