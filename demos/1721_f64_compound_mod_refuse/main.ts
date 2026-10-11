class F { rate: f64 = 2.5; }
function main(): i32 {
  const f = new F();
  f.rate %= 2;
  return 0;
}
