function main(): i32 {
  const a: i32[] = [8, 3, 5];
  console.log(Math.max(...a) - Math.min(...a));
  return 0;
}
