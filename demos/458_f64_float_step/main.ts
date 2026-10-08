function main(): i32 {
  let s: f64 = 0;
  for (let i: f64 = 0; i < 2; i = i + 0.5) {
    s = s + 1;
  }
  console.log(s);
  return 0;
}
