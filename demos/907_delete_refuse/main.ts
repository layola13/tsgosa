function main(): i32 {
  const a: i32[] = [1, 2, 3];
  delete a[1];
  console.log(a.length);
  return 0;
}
