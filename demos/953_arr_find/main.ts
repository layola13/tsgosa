function main(): i32 {
  const a: i32[] = [5, 6, 7];
  console.log(a.find((v: i32) => v > 5) ?? -1);
  console.log(a.findIndex((v: i32) => v === 7));
  return 0;
}
