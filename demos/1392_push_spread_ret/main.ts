function main(): i32 {
  const a: i32[] = [1];
  const b: i32[] = [2, 3];
  const n: i32 = a.push(...b);
  console.log(n);
  return 0;
}
