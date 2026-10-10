function main(): i32 {
  const a: i32[] = [1, 2];
  a.push(...a);
  console.log(a.length);
  console.log(a[3]);
  return 0;
}
