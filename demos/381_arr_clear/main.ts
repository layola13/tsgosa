function main(): i32 {
  const a: number[] = [1, 2, 3];
  console.log(a.length);
  a.length = 0;
  console.log(a.length);
  a.push(9);
  console.log(a.length);
  console.log(a[0]);
  return 0;
}
