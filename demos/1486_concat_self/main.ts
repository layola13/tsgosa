function main(): i32 {
  const a: i32[] = [1, 2];
  const b: i32[] = a.concat(a);
  console.log(b.length);
  console.log(b[3]);
  return 0;
}
