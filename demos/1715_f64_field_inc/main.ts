class F { rate: f64 = 2.5; }
function main(): i32 {
  const f = new F();
  f.rate++;
  console.log(f.rate);
  return 0;
}
