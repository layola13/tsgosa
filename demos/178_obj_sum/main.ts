function main(): i32 {
  const a: number[] = [3, 1, 2];
  a.sort();
  const b: number[] = [1, 2, 3, 4];
  const c: number[] = b.slice(1, 3);
  console.log(a[0], a[2], c.length, c[1]);
  return 0;
}
