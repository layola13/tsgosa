function main(): i32 {
  const a: i32[] = [1];
  a.push(...[2, 3]);
  console.log(a.length);
  console.log(a[2]);
  return 0;
}
