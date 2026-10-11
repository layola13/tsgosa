class F { rate: f64 = 0; n: i32 = 0; m: i32 = 0; k: i32 = 0; constructor() { this.rate = 7 / 2; this.n = 7 % 3; this.m = 1 << 3; this.k = 2 * 3 + 1; } }
function main(): i32 {
  const f = new F();
  console.log(f.rate);
  console.log(f.n);
  console.log(f.m);
  console.log(f.k);
  return 0;
}
