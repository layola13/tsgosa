function main(): i32 {
  const a = new Array<i32>(3);
  console.log(a.length);
  console.log(Array.isArray(a) ? 1 : 0);
  const m = [1, 2, 3].map((x) => x * 2);
  console.log(m[1]);
  const f = new Array<i32>(2);
  f.fill(7);
  console.log(f[0]);
  console.log(f[1]);
  return 0;
}
