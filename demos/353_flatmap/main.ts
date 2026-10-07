function main(): i32 {
  const a: number[] = [1, 2, 3];
  const b = a.flatMap((x) => [x, x * 10]);
  console.log(b.length);
  console.log(b[0]);
  console.log(b[1]);
  console.log(b[5]);
  const e: number[] = [];
  const f = e.flatMap((x) => [x]);
  console.log(f.length);
  const g = a.flatMap(((x) => [x]));
  console.log(g.length);
  console.log(g[2]);
  return 0;
}
