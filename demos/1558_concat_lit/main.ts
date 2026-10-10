function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const b: i32[] = a.concat([4, 5]);
  console.log(b.length);
  console.log(b[4]);
  return 0;
}
