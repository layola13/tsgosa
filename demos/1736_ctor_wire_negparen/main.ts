class F { rate: f64 = 0; n: i32 = 0; constructor() { this.rate = -(1.5 + 1); this.n = (2 + 3) * 2; } }
function main(): i32 {
  const f = new F();
  console.log(f.rate);
  console.log(f.n);
  return 0;
}
