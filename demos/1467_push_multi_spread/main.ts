function main(): i32 {
  const a: i32[] = [0];
  const b: i32[] = [1, 2];
  const c: i32[] = [3];
  a.push(...b, ...c);
  console.log(a.length);
  console.log(a[3]);
  return 0;
}
