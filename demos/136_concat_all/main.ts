function main(): i32 {
  const a: number[] = [1];
  const b: number[] = [2, 3];
  const c: number[] = a.concat(b);
  console.log(c.length, c[0], c[2]);
  return 0;
}
