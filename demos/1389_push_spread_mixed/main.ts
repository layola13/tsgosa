function main(): i32 {
  const a: i32[] = [1];
  a.push(0, ...[2, 3], 4);
  console.log(a.length);
  console.log(a[0]);
  console.log(a[3]);
  return 0;
}
