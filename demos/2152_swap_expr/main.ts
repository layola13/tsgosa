function main(): i32 {
  let a: i32 = 1;
  let b: i32 = 2;
  let c: i32 = 3;
  [a, b, c] = [c + 10, b + 10, a + 10];
  console.log(a);
  console.log(b);
  console.log(c);
  return 0;
}
