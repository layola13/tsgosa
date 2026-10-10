function main(): i32 {
  const a: i32[] = [9];
  const b: i32[] = [1, 2];
  const c: i32[] = [3];
  a.unshift(...b, ...c);
  console.log(a.length);
  console.log(a[0]);
  console.log(a[2]);
  return 0;
}
