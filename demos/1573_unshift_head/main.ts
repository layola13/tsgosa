function main(): i32 {
  const a: i32[] = [1, 2];
  a.unshift(0);
  console.log(a[0]);
  console.log(a.length);
  return 0;
}
