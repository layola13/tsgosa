function main(): i32 {
  const a: i32[] | null = null;
  console.log(a?.[0] ?? -1);
  const b: i32[] | null = [7, 8];
  console.log(b?.[1] ?? -1);
  return 0;
}
