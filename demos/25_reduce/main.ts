function main(): i32 {
  const a: number[] = [1, 2, 3, 4];
  const s = a.reduce((acc, x) => acc + x, 0);
  console.log(s);
  const p = a.reduceRight((acc, x) => acc * x, 1);
  console.log(p);
  return 0;
}
