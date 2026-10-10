function main(): i32 {
  const a: i32[] = [1, 3];
  a.splice(1, 0, 2);
  console.log(a.length);
  console.log(a[1]);
  return 0;
}
