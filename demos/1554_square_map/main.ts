function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const b: i32[] = a.map((x) => x * x);
  console.log(b[2]);
  return 0;
}
