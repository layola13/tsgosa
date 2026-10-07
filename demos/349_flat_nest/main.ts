function main(): i32 {
  const a: number[][] = [[1, 2], [3]];
  const f = a.flat();
  console.log(f.length);
  console.log(f[0] + f[2]);
  const b: number[] = [1, 2, 3, 4];
  const s = b.toSpliced(1, 2);
  console.log(s.length);
  console.log(s[0] + s[1]);
  return 0;
}
