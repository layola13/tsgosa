class F { n: i32 = 0; rate: f64 = 0; constructor(p) { this.n = p; this.rate = p * 2; } }
function main(): i32 {
  const f = new F(8);
  console.log(f.n);
  console.log(f.rate);
  return 0;
}
