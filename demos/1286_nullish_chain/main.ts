function main(): i32 {
  const a: i32 | null = null;
  const b: i32 = a ?? 8;
  console.log(b);
  const c: i32 | null = 5;
  console.log(c ?? 8);
  return 0;
}
