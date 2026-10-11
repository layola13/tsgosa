class F { rate: f64 = 0; constructor() { this.rate = -2.5; } }
function main(): i32 {
  const f = new F();
  console.log(f.rate);
  return 0;
}
