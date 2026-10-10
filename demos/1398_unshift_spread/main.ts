function main(): i32 {
  const a: i32[] = [3];
  a.unshift(...[1, 2]);
  console.log(a.length);
  console.log(a[0]);
  console.log(a[1]);
  return 0;
}
