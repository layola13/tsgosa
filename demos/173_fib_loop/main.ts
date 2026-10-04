function main(): i32 {
  let a: i32 = 0;
  let b: i32 = 1;
  for (let i: i32 = 0; i < 10; i++) {
    const t: i32 = a + b;
    a = b;
    b = t;
  }
  console.log(a);
  return 0;
}