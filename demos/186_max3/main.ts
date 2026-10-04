function main(): i32 {
  const a: i32 = 7;
  const b: i32 = 12;
  const c: i32 = 9;
  console.log(Math.max(a, b), Math.min(a, c), Math.max(a, Math.min(b, c)));
  return 0;
}