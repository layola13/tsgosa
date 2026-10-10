function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const f = (i: i32): i32 => a[i] * 2;
  console.log(f(0) + f(2));
  return 0;
}
