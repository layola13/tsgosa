class F { rate: f64 = 1; n: i32 = 1; constructor(k: i32) { this.rate = 2.5; this.n = k; } }
function main(): i32 {
  const f = new F(8);
  console.log(f.rate);
  console.log(f.n);
  return 0;
}
