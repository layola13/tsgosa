function main(): i32 {
  const a: number[] = [1, 2, 3, 4];
  const t: i32 = a.reduceRight((p: i32, c: i32) => p - c, 0);
  console.log(t);
  return 0;
}
