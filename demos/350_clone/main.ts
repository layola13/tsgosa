function main(): number {
  const a: number[] = [4, 5];
  const c = structuredClone(a);
  console.log(c.length + c[0]);
  const n: number[][] = [[1, 2], [3]];
  const d = structuredClone(n);
  console.log(d.length);
  return 0;
}
