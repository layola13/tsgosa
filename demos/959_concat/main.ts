function main(): i32 {
  const a: i32[] = [1, 2];
  const b: i32[] = [3, 4];
  const c = a.concat(b);
  console.log(c.length);
  console.log(c[3]);
  return 0;
}
