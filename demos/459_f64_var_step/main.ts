function main(): i32 {
  let s: f64 = 0;
  const st: f64 = 0.5;
  for (let i: f64 = 0; i < 2; i = i + st) {
    s = s + 1;
  }
  console.log(s);
  return 0;
}
