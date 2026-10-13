function main(): i32 {
  let a: i32 = 1;
  let b: i32 = 2;
  [a, b] = [b, a];
  console.log(a);
  console.log(b);
  return 0;
}
