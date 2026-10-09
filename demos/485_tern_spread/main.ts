function main(): i32 {
  const a = [1, 2, 3];
  const b = [0, ...(a[0] > 0 ? [7] : [8])];
  console.log(b.length);
  console.log(b[1]);
  const t = [7];
  const f = [8, 9];
  const c = [0, ...(a[0] > 5 ? t : f)];
  console.log(c.length);
  console.log(c[1]);
  console.log(c[2]);
  return 0;
}
