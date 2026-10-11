class F { rate: f64 = 0; constructor(p) { this.rate = p; } }
function main(): i32 {
  const f = new F(1.5);
  console.log(f.rate);
  return 0;
}
