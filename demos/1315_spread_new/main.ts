function main(): i32 {
  const a: i32[] = [...new Array<i32>(4, 5)];
  console.log(a.length);
  console.log(a[0]);
  return 0;
}
