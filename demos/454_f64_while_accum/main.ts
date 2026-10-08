function main(): i32 {
  let x: f64 = 0;
  let i = 0;
  while (i < 3) {
    x = x + 1.5;
    i = i + 1;
  }
  console.log(x);
  return 0;
}
