function main(): i32 {
  let a: i32 = 3;
  let b: i32 = 7;
  const t: i32 = a;
  a = b;
  b = t;
  console.log(a, b);
  return 0;
}