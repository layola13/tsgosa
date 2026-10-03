function main(): i32 {
  const a: number[] = [3, 1, 4, 1, 5];
  console.log(a.length);
  console.log(a[0] + a[4]);
  a[1] = 9;
  console.log(a[1]);
  return 0;
}
