function main(): i32 {
  let a: i32 = 0;
  let b: i32 = 1;
  for (let i: i32 = 0; i < 9; i++) {
    const c: i32 = a + b;
    a = b;
    b = c;
  }
  console.log(b);
  return 0;
}
