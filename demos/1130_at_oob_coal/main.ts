function main(): i32 {
  const a: i32[] = [1, 2];
  console.log(a.at(9) ?? -1);
  console.log(a.at(-9) ?? -1);
  return 0;
}
