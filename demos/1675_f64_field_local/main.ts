class F { rate: f64 = 2.5; n: i32 = 4; }
function main(): i32 {
  const f = new F();
  const r: f64 = f.rate;
  console.log(r + r);
  console.log(f.n);
  return 0;
}
