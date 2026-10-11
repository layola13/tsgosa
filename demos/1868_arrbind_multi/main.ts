function main(): i32 {
  const p: i32[] = [8];
  const [a = 1, b = 2] = p;
  console.log(a + b);
  return 0;
}
