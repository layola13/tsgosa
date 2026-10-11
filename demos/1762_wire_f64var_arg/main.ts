class F { rate: f64 = 0; constructor(p) { this.rate = p * 2; } }
function main(): i32 {
  const x: f64 = 1.5;
  const f = new F(x);
  console.log(f.rate);
  return 0;
}
