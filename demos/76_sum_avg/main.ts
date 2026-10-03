function main(): i32 {
  const a: number[] = [2, 4, 6, 8];
  let s: i32 = 0;
  for (const x of a) {
    s = s + x;
  }
  console.log(s / 4);
  return 0;
}
