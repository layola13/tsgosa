class F { rate: f64 = 0; n: i32 = 0; constructor() { this.rate = 2.5; this.n = 7; } }
function main(): i32 {
  const f = new F();
  console.log(f.rate);
  console.log(f.n);
  return 0;
}
