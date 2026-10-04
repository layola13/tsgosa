function main(): i32 {
  let x: i32 = 0;
  for (let i: i32 = 0; i < 8; i++) {
    x = x * 2 + (i % 2);
  }
  console.log(x);
  return 0;
}