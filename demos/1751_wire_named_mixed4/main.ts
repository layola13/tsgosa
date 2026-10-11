class F { rate: f64 = 0; n: i32 = 0; s: string = ""; constructor(p: f64, q: f64, k: i32, t: string) { this.rate = p + q; this.n = k * k; this.s = t + "!" + "?"; } }
function main(): i32 {
  const f = new F(1.5, 2.5, 4, "hi");
  console.log(f.rate);
  console.log(f.n);
  console.log(f.s);
  return 0;
}
