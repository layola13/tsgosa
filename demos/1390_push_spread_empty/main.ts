function main(): i32 {
  const a: i32[] = [1];
  const e: i32[] = [];
  console.log(a.push(...e));
  console.log(a[0]);
  return 0;
}
