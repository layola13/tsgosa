function main(): i32 {
  const a: i32[] = [9, 7, 5, 3, 1];
  console.log(a.find((x) => x < 6) ?? -1);
  return 0;
}
