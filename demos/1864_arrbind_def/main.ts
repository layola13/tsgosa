function main(): i32 {
  const a: i32[] = [1, 2];
  const [x = 5] = a;
  console.log(x);
  return 0;
}
