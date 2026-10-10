function main(): i32 {
  const a: i32[] = [1, 2, 3];
  a.splice(1, 1);
  console.log(a.length);
  console.log(a[1]);
  return 0;
}
