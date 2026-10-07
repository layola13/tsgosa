function main(): i32 {
  const a: number[] = [1, 2, 3];
  a.fill(9, 1);
  console.log(a[1]);
  const b: number[] = [1, 2, 3];
  b.fill(9, 1, 2);
  console.log(b[2]);
  const c: number[] = [1, 2, 3];
  c.fill(9, -2, -1);
  console.log(c[1]);
  const d: number[] = [1, 2, 3];
  d.fill(9);
  console.log(d[0]);
  return 0;
}
