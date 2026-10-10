function main(): i32 {
  const a: i32[] = [1, 2, 3];
  console.log(a.splice(1, 1, 7, 8).length);
  console.log(a.length);
  console.log(a[1]);
  return 0;
}
