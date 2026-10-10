function main(): i32 {
  const a: i32 | null = null;
  const b: i32 | null = 4;
  console.log(a ?? b ?? 5);
  return 0;
}
