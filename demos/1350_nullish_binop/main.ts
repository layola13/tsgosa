function main(): i32 {
  const a: i32 | null = null;
  console.log(a ?? (1 + 2));
  return 0;
}
