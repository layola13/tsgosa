function main(): i32 {
  const a: i32[] = [1, 2];
  const b: i32[] = a.flat(0);
  console.log(b.length);
  console.log(b[1]);
  return 0;
}
