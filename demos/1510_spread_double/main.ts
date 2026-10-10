function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const b: i32[] = [4, 5];
  const c: i32[] = [...a, ...b];
  console.log(c.length);
  console.log(c[3]);
  return 0;
}
