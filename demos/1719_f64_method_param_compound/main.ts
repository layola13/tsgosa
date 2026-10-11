class F { rate: f64 = 2.5; bump(d: f64): void { this.rate += d; } }
function main(): i32 {
  const f = new F();
  const st: f64 = 1.5;
  f.bump(st);
  console.log(f.rate);
  return 0;
}
