function main(): i32 {
  const a: i32[] = [10, 20];
  console.log(a[1 && 1]);
  console.log(a[0 || 1]);
  return 0;
}
