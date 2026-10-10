function main(): i32 {
  const a: i32 | null = null;
  const c: i32 = 1;
  console.log((a ?? 0) + (c ? 1 : 2));
  return 0;
}
