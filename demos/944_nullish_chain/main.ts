function main(): i32 {
  const a: i32|null = null;
  const b: i32|null = 5;
  console.log(a ?? b ?? 9);
  console.log(b ?? 9);
  return 0;
}
