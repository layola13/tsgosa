function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const b: i32[] = a.slice(-1);
  console.log(b.length);
  console.log(b[0]);
  return 0;
}
